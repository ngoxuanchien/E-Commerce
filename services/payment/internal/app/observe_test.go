package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/postgres"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/domain"
	"github.com/ngoxuanchien/e-commerce/services/payment/migrations"
)

var (
	ctx = context.Background()
	t0  = time.Now().UTC().Truncate(time.Microsecond)
)

func id() string { return uuid.Must(uuid.NewV7()).String() }

// stubProvider is a scriptable Provider for use-case tests.
type stubProvider struct {
	mu       sync.Mutex
	name     string
	observed domain.Observed
	queryErr error
	cancels  int
	refunds  []app.RefundInput
	refund   app.RefundObserved
}

func (s *stubProvider) Name() string { return s.name }
func (s *stubProvider) CreateSession(context.Context, app.SessionInput) (app.Session, error) {
	return app.Session{RedirectURL: "https://pay.example/s", ProviderRef: id()}, nil
}
func (s *stubProvider) Query(context.Context, domain.Payment) (domain.Observed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.observed, s.queryErr
}
func (s *stubProvider) Cancel(context.Context, domain.Payment) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancels++
	return true, nil
}
func (s *stubProvider) Refund(_ context.Context, in app.RefundInput) (app.RefundObserved, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refunds = append(s.refunds, in)
	return s.refund, nil
}
func (s *stubProvider) QueryRefund(context.Context, app.Refund) (app.RefundObserved, error) {
	return s.refund, nil
}

var _ app.Provider = (*stubProvider)(nil)

type env struct {
	store *postgres.Store
	pool  *pgxpool.Pool
	obs   *app.Observer
}

func newEnv(t *testing.T) env {
	t.Helper()
	dsn := testutil.Postgres(t)
	require.NoError(t, pgxx.Migrate(dsn, migrations.FS))
	pool, err := pgxx.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	s := postgres.NewStore(pool)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return env{store: s, pool: pool, obs: app.NewObserver(s, func() time.Time { return t0.Add(time.Hour) }, log)}
}

func newPayment(checkout string, status domain.Status, amounts ...int64) app.NewPayment {
	if len(amounts) == 0 {
		amounts = []int64{300_000}
	}
	var total int64
	var allocs []app.Allocation
	for _, a := range amounts {
		total += a
		allocs = append(allocs, app.Allocation{OrderID: id(), SellerUserID: id(), Amount: a, CommissionAmount: a / 20})
	}
	return app.NewPayment{
		Payment: domain.Payment{
			ID: id(), CheckoutID: checkout, BuyerID: id(), Method: "VNPAY", Provider: "fake", Status: status,
			Amount: total, ProviderRef: "ref-" + id(), ExpiresAt: t0.Add(15 * time.Minute), CreatedAt: t0, UpdatedAt: t0,
		},
		Allocations: allocs,
	}
}

func (e env) insert(t *testing.T, ps ...app.NewPayment) {
	t.Helper()
	for _, p := range ps {
		require.NoError(t, e.store.InTx(ctx, func(tx app.Tx) error { return tx.InsertPayment(ctx, p) }))
	}
}

func success(p app.NewPayment) domain.Observed {
	return domain.Observed{State: domain.ObservedSucceeded, Amount: p.Amount, ProviderTxnID: "txn-" + p.ID}
}

type outboxRow struct {
	Type string
	Raw  []byte
}

func (e env) outbox(t *testing.T, checkout string) []outboxRow {
	t.Helper()
	rows, err := e.pool.Query(ctx, `SELECT payload FROM outbox WHERE msg_key = $1 ORDER BY id`, checkout)
	require.NoError(t, err)
	defer rows.Close()
	var out []outboxRow
	for rows.Next() {
		var b []byte
		require.NoError(t, rows.Scan(&b))
		var head struct {
			EventType string `json:"event_type"`
		}
		require.NoError(t, json.Unmarshal(b, &head))
		out = append(out, outboxRow{Type: head.EventType, Raw: b})
	}
	require.NoError(t, rows.Err())
	return out
}

func (e env) refunds(t *testing.T, paymentID string) []app.Refund {
	t.Helper()
	rows, err := e.pool.Query(ctx, `SELECT order_id::text, amount, status, next_attempt_at FROM refunds WHERE payment_id = $1`, paymentID)
	require.NoError(t, err)
	defer rows.Close()
	var out []app.Refund
	for rows.Next() {
		var r app.Refund
		require.NoError(t, rows.Scan(&r.OrderID, &r.Amount, &r.Status, &r.NextAttemptAt))
		out = append(out, r)
	}
	return out
}

func (e env) cancel(t *testing.T, orderID, checkout string) {
	t.Helper()
	require.NoError(t, e.store.InTx(ctx, func(tx app.Tx) error {
		_, err := tx.RecordCancellation(ctx, orderID, checkout, t0)
		return err
	}))
}

func validate(t *testing.T, file string, raw []byte) {
	t.Helper()
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	sch, err := c.Compile(filepath.Join("..", "..", "..", "..", "api", "events", file))
	require.NoError(t, err)
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	require.NoError(t, err)
	assert.NoError(t, sch.Validate(inst), string(raw))
}

// runAll runs fn(i) in n goroutines and returns their errors.
func runAll(n int, fn func(i int) error) []error {
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = fn(i)
		}()
	}
	wg.Wait()
	return errs
}

func TestApplyObserved(t *testing.T) {
	e := newEnv(t)

	t.Run("success captures, publishes once and matches the schema", func(t *testing.T) {
		p := newPayment(id(), domain.StatusPending)
		e.insert(t, p)
		out, err := e.obs.ApplyObserved(ctx, "fake", "k1", p.ID, success(p))
		require.NoError(t, err)
		assert.False(t, out.Unchanged)
		assert.Equal(t, domain.StatusSucceeded, out.Payment.Status)
		assert.True(t, out.Payment.Captured)

		got, err := e.store.GetPayment(ctx, p.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusSucceeded, got.Status)
		assert.True(t, got.Captured)
		assert.Equal(t, "txn-"+p.ID, got.ProviderTxnID)

		ob := e.outbox(t, p.CheckoutID)
		require.Len(t, ob, 1)
		assert.Equal(t, "PaymentSucceeded", ob[0].Type)
		validate(t, "payment-succeeded.schema.json", ob[0].Raw)
		var ev struct {
			Data event.PaymentSucceeded `json:"data"`
		}
		require.NoError(t, json.Unmarshal(ob[0].Raw, &ev))
		assert.Equal(t, "txn-"+p.ID, ev.Data.ProviderRef)
		require.Len(t, ev.Data.Allocations, 1)
		a := p.Allocations[0]
		assert.Equal(t, event.Allocation{OrderID: a.OrderID, SellerUserID: a.SellerUserID, Amount: a.Amount, CommissionAmount: a.CommissionAmount}, ev.Data.Allocations[0])
		assert.Empty(t, e.refunds(t, p.ID))
	})

	t.Run("same event key twice is Unchanged with one outbox row", func(t *testing.T) {
		p := newPayment(id(), domain.StatusPending)
		e.insert(t, p)
		_, err := e.obs.ApplyObserved(ctx, "fake", "dup", p.ID, success(p))
		require.NoError(t, err)
		out, err := e.obs.ApplyObserved(ctx, "fake", "dup", p.ID, success(p))
		require.NoError(t, err)
		assert.True(t, out.Unchanged)
		assert.Len(t, e.outbox(t, p.CheckoutID), 1)
	})

	t.Run("concurrent identical callbacks apply once", func(t *testing.T) {
		p := newPayment(id(), domain.StatusPending)
		e.insert(t, p)
		for _, err := range runAll(8, func(int) error {
			_, err := e.obs.ApplyObserved(ctx, "fake", "same", p.ID, success(p))
			return err
		}) {
			require.NoError(t, err)
		}
		assert.Len(t, e.outbox(t, p.CheckoutID), 1)
	})

	t.Run("concurrent different keys still emit one event", func(t *testing.T) {
		p := newPayment(id(), domain.StatusPending)
		e.insert(t, p)
		for _, err := range runAll(8, func(int) error {
			_, err := e.obs.ApplyObserved(ctx, "fake", id(), p.ID, success(p))
			return err
		}) {
			require.NoError(t, err)
		}
		assert.Len(t, e.outbox(t, p.CheckoutID), 1)
		assert.Empty(t, e.refunds(t, p.ID))
	})

	t.Run("another success with a different key emits no second event", func(t *testing.T) {
		p := newPayment(id(), domain.StatusPending)
		e.insert(t, p)
		_, err := e.obs.ApplyObserved(ctx, "fake", "a", p.ID, success(p))
		require.NoError(t, err)
		out, err := e.obs.ApplyObserved(ctx, "fake", "b", p.ID, success(p))
		require.NoError(t, err)
		assert.True(t, out.Unchanged)
		assert.Len(t, e.outbox(t, p.CheckoutID), 1)
	})

	t.Run("second success in one checkout is DUPLICATE with a full refund", func(t *testing.T) {
		co := id()
		first := newPayment(co, domain.StatusFailed)
		second := newPayment(co, domain.StatusPending)
		e.insert(t, first, second)
		_, err := e.obs.ApplyObserved(ctx, "fake", "", first.ID, success(first))
		require.NoError(t, err)
		out, err := e.obs.ApplyObserved(ctx, "fake", "", second.ID, success(second))
		require.NoError(t, err)
		assert.Equal(t, domain.StatusDuplicate, out.Payment.Status)
		assert.False(t, out.Payment.Captured)

		ob := e.outbox(t, co)
		require.Len(t, ob, 2)
		validate(t, "payment-succeeded.schema.json", ob[1].Raw)
		var ev struct {
			Data map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal(ob[1].Raw, &ev))
		assert.Equal(t, second.ID, ev.Data["payment_id"])
		assert.Equal(t, []any{}, ev.Data["allocations"])

		rs := e.refunds(t, second.ID)
		require.Len(t, rs, 1)
		assert.Nil(t, rs[0].OrderID)
		assert.Equal(t, second.Amount, rs[0].Amount)
		assert.Equal(t, "PENDING", rs[0].Status)
		assert.True(t, rs[0].NextAttemptAt.Equal(t0.Add(time.Hour)))
		assert.Empty(t, e.refunds(t, first.ID))
	})

	t.Run("concurrent successes of two payments capture exactly one", func(t *testing.T) {
		co := id()
		ps := []app.NewPayment{newPayment(co, domain.StatusFailed), newPayment(co, domain.StatusPending)}
		e.insert(t, ps...)
		for _, err := range runAll(2, func(i int) error {
			_, err := e.obs.ApplyObserved(ctx, "fake", id(), ps[i].ID, success(ps[i]))
			return err
		}) {
			require.NoError(t, err)
		}
		rows, err := e.store.ListCheckoutPayments(ctx, co)
		require.NoError(t, err)
		captured, dup := 0, 0
		for _, r := range rows {
			if r.Captured {
				captured++
			}
			if r.Status == domain.StatusDuplicate {
				dup++
			}
		}
		assert.Equal(t, 1, captured)
		assert.Equal(t, 1, dup)
		assert.Equal(t, 1, len(e.refunds(t, ps[0].ID))+len(e.refunds(t, ps[1].ID)))
	})

	t.Run("success wins over EXPIRED", func(t *testing.T) {
		p := newPayment(id(), domain.StatusExpired)
		e.insert(t, p)
		out, err := e.obs.ApplyObserved(ctx, "fake", "", p.ID, success(p))
		require.NoError(t, err)
		assert.Equal(t, domain.StatusSucceeded, out.Payment.Status)
		assert.True(t, out.Payment.Captured)
	})

	t.Run("amount mismatch changes nothing", func(t *testing.T) {
		p := newPayment(id(), domain.StatusPending)
		e.insert(t, p)
		obs := success(p)
		obs.Amount++
		out, err := e.obs.ApplyObserved(ctx, "fake", "", p.ID, obs)
		require.NoError(t, err)
		assert.True(t, out.Unchanged)
		assert.True(t, out.AmountMismatch)
		got, err := e.store.GetPayment(ctx, p.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusPending, got.Status)
		assert.Empty(t, e.outbox(t, p.CheckoutID))
	})

	t.Run("failure publishes PaymentFailed with the reason", func(t *testing.T) {
		p := newPayment(id(), domain.StatusPending)
		e.insert(t, p)
		out, err := e.obs.ApplyObserved(ctx, "fake", "f", p.ID, domain.Observed{State: domain.ObservedFailed, Reason: "declined"})
		require.NoError(t, err)
		assert.Equal(t, domain.StatusFailed, out.Payment.Status)
		ob := e.outbox(t, p.CheckoutID)
		require.Len(t, ob, 1)
		assert.Equal(t, "PaymentFailed", ob[0].Type)
		validate(t, "payment-failed.schema.json", ob[0].Raw)
		var ev struct {
			Data event.PaymentFailed `json:"data"`
		}
		require.NoError(t, json.Unmarshal(ob[0].Raw, &ev))
		assert.Equal(t, "declined", ev.Data.Reason)
	})

	t.Run("pending and unknown payments", func(t *testing.T) {
		p := newPayment(id(), domain.StatusPending)
		e.insert(t, p)
		out, err := e.obs.ApplyObserved(ctx, "fake", "", p.ID, domain.Observed{State: domain.ObservedPending})
		require.NoError(t, err)
		assert.True(t, out.Unchanged)
		_, err = e.obs.ApplyObserved(ctx, "fake", "", id(), domain.Observed{State: domain.ObservedPending})
		assert.ErrorIs(t, err, app.ErrNotFound)
	})

	t.Run("observed expiry only updates the payment", func(t *testing.T) {
		p := newPayment(id(), domain.StatusPending)
		e.insert(t, p)
		out, err := e.obs.ApplyObserved(ctx, "fake", "", p.ID, domain.Observed{State: domain.ObservedExpired})
		require.NoError(t, err)
		assert.Equal(t, domain.StatusExpired, out.Payment.Status)
		assert.Empty(t, e.outbox(t, p.CheckoutID))
	})

	t.Run("capture of a payment whose order is already cancelled refunds that allocation only", func(t *testing.T) {
		p := newPayment(id(), domain.StatusPending, 100_000, 200_000)
		e.insert(t, p)
		e.cancel(t, p.Allocations[1].OrderID, p.CheckoutID)
		_, err := e.obs.ApplyObserved(ctx, "fake", "", p.ID, success(p))
		require.NoError(t, err)
		rs := e.refunds(t, p.ID)
		require.Len(t, rs, 1)
		require.NotNil(t, rs[0].OrderID)
		assert.Equal(t, p.Allocations[1].OrderID, *rs[0].OrderID)
		assert.Equal(t, int64(200_000), rs[0].Amount)
		assert.Len(t, e.outbox(t, p.CheckoutID), 1)
	})
}

func TestExpire(t *testing.T) {
	e := newEnv(t)
	p := newPayment(id(), domain.StatusPending)
	e.insert(t, p)
	got, err := e.obs.Expire(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusExpired, got.Status)
	stored, err := e.store.GetPayment(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusExpired, stored.Status)

	s := newPayment(id(), domain.StatusSucceeded)
	s.Captured = true
	e.insert(t, s)
	got, err = e.obs.Expire(ctx, s.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusSucceeded, got.Status)

	_, err = e.obs.Expire(ctx, id())
	assert.ErrorIs(t, err, app.ErrNotFound)
}

func TestProviders(t *testing.T) {
	v, s := &stubProvider{name: "vnpay"}, &stubProvider{name: "fake"}
	ps := app.NewProviders(v, s)
	got, ok := ps.ForMethod("VNPAY")
	assert.True(t, ok)
	assert.Same(t, v, got)
	got, ok = ps.ForMethod("STRIPE")
	assert.True(t, ok)
	assert.Same(t, s, got)
	_, ok = ps.ForMethod("COD")
	assert.False(t, ok)
	got, ok = ps.ByName("fake")
	assert.True(t, ok)
	assert.Same(t, s, got)
	_, ok = ps.ByName("stripe")
	assert.False(t, ok)
}

func TestCaptureSkipsZeroAmountAllocations(t *testing.T) {
	e := newEnv(t)
	p := newPayment(id(), domain.StatusPending, 0, 100_000)
	e.insert(t, p)
	// Both orders were cancelled before the capture; the zero allocation has nothing to refund.
	e.cancel(t, p.Allocations[0].OrderID, p.CheckoutID)
	e.cancel(t, p.Allocations[1].OrderID, p.CheckoutID)

	_, err := e.obs.ApplyObserved(ctx, "fake", id(), p.ID, success(p))
	require.NoError(t, err)

	got, err := e.store.GetPayment(ctx, p.ID)
	require.NoError(t, err)
	assert.True(t, got.Captured)
	rs := e.refunds(t, p.ID)
	require.Len(t, rs, 1)
	assert.Equal(t, p.Allocations[1].OrderID, *rs[0].OrderID)
	assert.EqualValues(t, 100_000, rs[0].Amount)
}
