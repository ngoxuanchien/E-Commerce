package fake_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/fake"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/domain"
	"github.com/ngoxuanchien/e-commerce/services/payment/migrations"
)

const secret = "whsec"

var ctx = context.Background()

func id() string { return uuid.Must(uuid.NewV7()).String() }

type harness struct {
	pool *pgxpool.Pool
	mu   sync.Mutex
	now  time.Time
	got  chan fake.Event
	bad  chan error
	srv  *httptest.Server
}

func (h *harness) clock() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.now
}

func (h *harness) advance(d time.Duration) {
	h.mu.Lock()
	h.now = h.now.Add(d)
	h.mu.Unlock()
}

func newHarness(t *testing.T) (*harness, *fake.Provider) {
	dsn := testutil.Postgres(t)
	require.NoError(t, pgxx.Migrate(dsn, migrations.FS))
	pool, err := pgxx.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	h := &harness{pool: pool, now: time.Now().UTC().Truncate(time.Microsecond), got: make(chan fake.Event, 16), bad: make(chan error, 16)}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := fake.Verify(secret, body, r.Header.Get("X-Fake-Signature"), h.clock()); err != nil {
			h.bad <- err
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var ev fake.Event
		if err := json.Unmarshal(body, &ev); err != nil {
			h.bad <- err
		}
		h.got <- ev
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(h.srv.Close)

	p := fake.New(pool, fake.Config{
		PublicBaseURL: "http://gw.test", WebhookURL: h.srv.URL, WebhookSecret: secret,
		WebhookDelay: 20 * time.Millisecond, Now: h.clock, HTTPClient: h.srv.Client(),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	t.Cleanup(p.Wait) // registered last, runs first: no delivery goroutine outlives the test
	return h, p
}

// seed inserts a PENDING payment row (fake_sessions has an FK to payments).
func (h *harness) seed(t *testing.T, amount int64, expires time.Time) domain.Payment {
	t.Helper()
	now := h.clock()
	pay := domain.Payment{
		ID: id(), CheckoutID: id(), BuyerID: id(), Method: "VNPAY", Provider: "fake",
		Status: domain.StatusPending, Amount: amount, ExpiresAt: expires, CreatedAt: now, UpdatedAt: now,
	}
	_, err := h.pool.Exec(ctx, `INSERT INTO payments (id, checkout_id, buyer_id, method, provider, status, amount, expires_at, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		pay.ID, pay.CheckoutID, pay.BuyerID, pay.Method, pay.Provider, string(pay.Status), pay.Amount, pay.ExpiresAt, pay.CreatedAt, pay.UpdatedAt)
	require.NoError(t, err)
	return pay
}

func (h *harness) session(t *testing.T, p *fake.Provider, pay domain.Payment) {
	t.Helper()
	s, err := p.CreateSession(ctx, app.SessionInput{PaymentID: pay.ID, CheckoutID: pay.CheckoutID, Amount: pay.Amount, ExpiresAt: pay.ExpiresAt})
	require.NoError(t, err)
	assert.Equal(t, "http://gw.test/api/payments/fake/checkout/"+pay.ID, s.RedirectURL)
	assert.Equal(t, "fake_"+pay.ID, s.ProviderRef)
}

func (h *harness) next(t *testing.T) fake.Event {
	t.Helper()
	select {
	case ev := <-h.got:
		return ev
	case err := <-h.bad:
		t.Fatalf("webhook rejected: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("no webhook received")
	}
	return fake.Event{}
}

func (h *harness) none(t *testing.T, within time.Duration) {
	t.Helper()
	select {
	case ev := <-h.got:
		t.Fatalf("unexpected webhook %+v", ev)
	case <-time.After(within):
	}
}

func TestProviderLifecycle(t *testing.T) {
	h, p := newHarness(t)
	assert.Equal(t, "fake", p.Name())
	pay := h.seed(t, 150_000, h.clock().Add(15*time.Minute))
	h.session(t, p, pay)

	pd, err := p.Page(ctx, pay.ID)
	require.NoError(t, err)
	assert.Equal(t, fake.PageData{PaymentID: pay.ID, Amount: 150_000, ExpiresAt: pay.ExpiresAt}, pd)
	_, err = p.Page(ctx, id())
	assert.ErrorIs(t, err, app.ErrNotFound)
	_, err = p.Page(ctx, "not-a-uuid")
	assert.ErrorIs(t, err, app.ErrNotFound)

	o, err := p.Query(ctx, pay)
	require.NoError(t, err)
	assert.Equal(t, domain.ObservedPending, o.State)

	require.NoError(t, p.Submit(ctx, pay.ID, "succeed", fake.Hooks{}))
	ev := h.next(t)
	assert.Equal(t, fake.Event{EventID: ev.EventID, PaymentID: pay.ID, Outcome: "succeed"}, ev)
	assert.NotEmpty(t, ev.EventID)

	o, err = p.Query(ctx, pay)
	require.NoError(t, err)
	assert.Equal(t, domain.Observed{State: domain.ObservedSucceeded, Amount: 150_000, ProviderTxnID: "fake_txn_" + pay.ID}, o)

	require.ErrorIs(t, p.Submit(ctx, pay.ID, "fail", fake.Hooks{}), fake.ErrSessionClosed)
	require.ErrorIs(t, p.Submit(ctx, id(), "fail", fake.Hooks{}), app.ErrNotFound)
	require.Error(t, p.Submit(ctx, pay.ID, "bogus", fake.Hooks{}))
}

func TestQueryFailedAndExpired(t *testing.T) {
	h, p := newHarness(t)
	failed := h.seed(t, 10_000, h.clock().Add(time.Hour))
	h.session(t, p, failed)
	require.NoError(t, p.Submit(ctx, failed.ID, "fail", fake.Hooks{Webhook: "drop"}))
	o, err := p.Query(ctx, failed)
	require.NoError(t, err)
	assert.Equal(t, domain.Observed{State: domain.ObservedFailed, Reason: "fake_declined"}, o)

	exp := h.seed(t, 10_000, h.clock().Add(time.Minute))
	h.session(t, p, exp)
	o, err = p.Query(ctx, exp)
	require.NoError(t, err)
	assert.Equal(t, domain.ObservedPending, o.State)
	h.advance(time.Minute)
	o, err = p.Query(ctx, exp)
	require.NoError(t, err)
	assert.Equal(t, domain.ObservedExpired, o.State)
}

func TestCancel(t *testing.T) {
	h, p := newHarness(t)

	t.Run("already has an outcome", func(t *testing.T) {
		pay := h.seed(t, 10_000, h.clock().Add(time.Hour))
		h.session(t, p, pay)
		require.NoError(t, p.Submit(ctx, pay.ID, "succeed", fake.Hooks{Webhook: "drop"}))
		ok, err := p.Cancel(ctx, pay)
		require.NoError(t, err)
		assert.False(t, ok)
	})
	t.Run("unsupported", func(t *testing.T) {
		pay := h.seed(t, 10_000, h.clock().Add(time.Hour))
		h.session(t, p, pay)
		// Hooks normally arrive with Submit (which also records an outcome); set them alone to reach the branch.
		_, err := h.pool.Exec(ctx, `UPDATE fake_sessions SET hooks='{"cancel":"unsupported"}' WHERE payment_id=$1`, pay.ID)
		require.NoError(t, err)
		ok, err := p.Cancel(ctx, pay)
		require.NoError(t, err)
		assert.False(t, ok)
		o, err := p.Query(ctx, pay)
		require.NoError(t, err)
		assert.Equal(t, domain.ObservedPending, o.State, "session stays payable")
	})
	t.Run("cancelled", func(t *testing.T) {
		pay := h.seed(t, 10_000, h.clock().Add(time.Hour))
		h.session(t, p, pay)
		ok, err := p.Cancel(ctx, pay)
		require.NoError(t, err)
		assert.True(t, ok)
		o, err := p.Query(ctx, pay)
		require.NoError(t, err)
		assert.Equal(t, domain.ObservedExpired, o.State)
		require.ErrorIs(t, p.Submit(ctx, pay.ID, "succeed", fake.Hooks{}), fake.ErrSessionClosed)
	})
}

func TestRefund(t *testing.T) {
	h, p := newHarness(t)
	pay := h.seed(t, 100_000, h.clock().Add(time.Hour))
	h.session(t, p, pay)
	require.NoError(t, p.Submit(ctx, pay.ID, "succeed", fake.Hooks{Webhook: "drop"}))

	r, err := p.Refund(ctx, app.RefundInput{RefundID: "rf1", Payment: pay, Amount: 40_000})
	require.NoError(t, err)
	assert.Equal(t, app.RefundObserved{State: app.RefundSucceeded, ProviderRefundRef: "fake_rf_rf1"}, r)
	var refunded int64
	require.NoError(t, h.pool.QueryRow(ctx, `SELECT refunded FROM fake_sessions WHERE payment_id=$1`, pay.ID).Scan(&refunded))
	assert.EqualValues(t, 40_000, refunded)

	q, err := p.QueryRefund(ctx, app.Refund{ID: "rf1", PaymentID: pay.ID, Amount: 40_000})
	require.NoError(t, err)
	assert.Equal(t, app.RefundObserved{State: app.RefundSucceeded, ProviderRefundRef: "fake_rf_rf1"}, q)

	failing := h.seed(t, 100_000, h.clock().Add(time.Hour))
	h.session(t, p, failing)
	require.NoError(t, p.Submit(ctx, failing.ID, "succeed", fake.Hooks{Webhook: "drop", Refund: "fail"}))
	r, err = p.Refund(ctx, app.RefundInput{RefundID: "rf2", Payment: failing, Amount: 10_000})
	require.NoError(t, err)
	assert.Equal(t, app.RefundObserved{State: app.RefundFailed, Reason: "fake_refund_failed"}, r)
	q, err = p.QueryRefund(ctx, app.Refund{ID: "rf2", PaymentID: failing.ID, Amount: 10_000})
	require.NoError(t, err)
	assert.Equal(t, app.RefundFailed, q.State)
	require.NoError(t, h.pool.QueryRow(ctx, `SELECT refunded FROM fake_sessions WHERE payment_id=$1`, failing.ID).Scan(&refunded))
	assert.EqualValues(t, 0, refunded)
}

func TestSender(t *testing.T) {
	t.Run("duplicate", func(t *testing.T) {
		h, p := newHarness(t)
		pay := h.seed(t, 5_000, h.clock().Add(time.Hour))
		h.session(t, p, pay)
		require.NoError(t, p.Submit(ctx, pay.ID, "succeed", fake.Hooks{Webhook: "duplicate"}))
		a, b := h.next(t), h.next(t)
		assert.Equal(t, a, b)
		assert.Equal(t, "succeed", a.Outcome)
		h.none(t, 60*time.Millisecond)
	})
	t.Run("drop", func(t *testing.T) {
		h, p := newHarness(t)
		pay := h.seed(t, 5_000, h.clock().Add(time.Hour))
		h.session(t, p, pay)
		require.NoError(t, p.Submit(ctx, pay.ID, "succeed", fake.Hooks{Webhook: "drop"}))
		h.none(t, 60*time.Millisecond) // 3x delay
	})
	t.Run("stale_after", func(t *testing.T) {
		h, p := newHarness(t)
		pay := h.seed(t, 5_000, h.clock().Add(time.Hour))
		h.session(t, p, pay)
		require.NoError(t, p.Submit(ctx, pay.ID, "fail", fake.Hooks{Webhook: "stale_after"}))
		a, b := h.next(t), h.next(t)
		assert.Equal(t, "fail", a.Outcome)
		assert.Equal(t, "pending", b.Outcome)
		assert.Equal(t, pay.ID, b.PaymentID)
		assert.NotEqual(t, a.EventID, b.EventID)
		h.none(t, 60*time.Millisecond)
	})
}
