package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/fake"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/domain"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func cancelled(orderID, checkout, method string) event.OrderCancelled {
	return event.OrderCancelled{OrderID: orderID, Reason: "x", CancelledBy: event.CancelledByBuyer, CheckoutID: checkout, PaymentMethod: method}
}

func (e env) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, e.pool.QueryRow(ctx, query, args...).Scan(&n))
	return n
}

func TestRefundsOrderCancelled(t *testing.T) {
	e := newEnv(t)
	clk := &clock{t: t0.Add(time.Hour)}
	rf := app.NewRefunds(e.store, clk.now)

	t.Run("capture then cancel creates one refund for the allocation", func(t *testing.T) {
		p := newPayment(id(), domain.StatusPending, 100_000, 200_000)
		e.insert(t, p)
		_, err := e.obs.ApplyObserved(ctx, "fake", id(), p.ID, success(p))
		require.NoError(t, err)
		require.Empty(t, e.refunds(t, p.ID))
		o := p.Allocations[1]
		require.NoError(t, rf.OrderCancelled(ctx, id(), cancelled(o.OrderID, p.CheckoutID, "VNPAY"), t0))
		rs := e.refunds(t, p.ID)
		require.Len(t, rs, 1)
		assert.Equal(t, o.OrderID, *rs[0].OrderID)
		assert.EqualValues(t, 200_000, rs[0].Amount)
		assert.Equal(t, "PENDING", rs[0].Status)
		assert.True(t, rs[0].NextAttemptAt.Equal(clk.now()))
	})

	t.Run("cancel then capture creates one refund", func(t *testing.T) {
		p := newPayment(id(), domain.StatusPending, 100_000, 200_000)
		e.insert(t, p)
		o := p.Allocations[0]
		require.NoError(t, rf.OrderCancelled(ctx, id(), cancelled(o.OrderID, p.CheckoutID, "STRIPE"), t0))
		require.Empty(t, e.refunds(t, p.ID))
		_, err := e.obs.ApplyObserved(ctx, "fake", id(), p.ID, success(p))
		require.NoError(t, err)
		rs := e.refunds(t, p.ID)
		require.Len(t, rs, 1)
		assert.Equal(t, o.OrderID, *rs[0].OrderID)
		assert.EqualValues(t, 100_000, rs[0].Amount)
	})

	t.Run("a redelivered event id changes nothing", func(t *testing.T) {
		p := newPayment(id(), domain.StatusPending)
		e.insert(t, p)
		_, err := e.obs.ApplyObserved(ctx, "fake", id(), p.ID, success(p))
		require.NoError(t, err)
		ev, eid := cancelled(p.Allocations[0].OrderID, p.CheckoutID, "VNPAY"), id()
		require.NoError(t, rf.OrderCancelled(ctx, eid, ev, t0))
		require.NoError(t, rf.OrderCancelled(ctx, eid, ev, t0))
		assert.Len(t, e.refunds(t, p.ID), 1)
		assert.Equal(t, 1, e.count(t, `SELECT count(*) FROM order_cancellations WHERE order_id = $1`, ev.OrderID))
	})

	t.Run("COD and P1-era events are marked processed and otherwise skipped", func(t *testing.T) {
		p := newPayment(id(), domain.StatusPending)
		e.insert(t, p)
		_, err := e.obs.ApplyObserved(ctx, "fake", id(), p.ID, success(p))
		require.NoError(t, err)
		oid := p.Allocations[0].OrderID
		for name, ev := range map[string]event.OrderCancelled{
			"cod":         cancelled(oid, p.CheckoutID, "COD"),
			"p1 era":      cancelled(oid, "", ""),
			"no checkout": cancelled(oid, "", "VNPAY"),
		} {
			eid := id()
			require.NoError(t, rf.OrderCancelled(ctx, eid, ev, t0), name)
			assert.Equal(t, 1, e.count(t, `SELECT count(*) FROM processed_events WHERE event_id = $1`, eid), name)
		}
		assert.Equal(t, 0, e.count(t, `SELECT count(*) FROM order_cancellations WHERE order_id = $1`, oid))
		assert.Empty(t, e.refunds(t, p.ID))
	})

	t.Run("the cancellation takes the checkout lock before it looks at the captured allocation", func(t *testing.T) {
		p := newPayment(id(), domain.StatusPending)
		e.insert(t, p)
		orderID := p.Allocations[0].OrderID
		ev := cancelled(orderID, p.CheckoutID, "VNPAY")
		release, locked, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		var once sync.Once
		unlock := func() { once.Do(func() { close(release) }) }
		defer unlock()
		holder := make(chan error, 1)
		go func() {
			// A capture in flight: it holds the lock, marks the payment captured and checks the
			// cancellation, then commits.
			holder <- e.store.InTx(ctx, func(tx app.Tx) error {
				rows, err := tx.LockCheckoutPayments(ctx, p.CheckoutID)
				if err != nil {
					return err
				}
				close(locked)
				<-release
				cur, _ := findPayment(rows, p.ID)
				cur.Status, cur.Captured, cur.UpdatedAt = domain.StatusSucceeded, true, clk.now()
				if err := tx.UpdatePayment(ctx, cur); err != nil {
					return err
				}
				cancelledNow, err := tx.IsCancelled(ctx, orderID)
				if err != nil {
					return err
				}
				if cancelledNow {
					return errors.New("cancellation visible before the lock was released")
				}
				return nil
			})
		}()
		<-locked
		go func() { finished <- rf.OrderCancelled(ctx, id(), ev, t0) }()
		select {
		case err := <-finished:
			unlock()
			t.Fatalf("OrderCancelled did not wait for the lock (err=%v)", err)
		case <-time.After(300 * time.Millisecond):
		}
		unlock()
		require.NoError(t, <-holder)
		require.NoError(t, <-finished)
		rs := e.refunds(t, p.ID)
		require.Len(t, rs, 1, "the cancellation must see the capture that committed while it waited")
		assert.Equal(t, orderID, *rs[0].OrderID)
	})

	t.Run("concurrent capture and cancellation always yield exactly one refund", func(t *testing.T) {
		for i := 0; i < 10; i++ {
			p := newPayment(id(), domain.StatusPending)
			e.insert(t, p)
			ev := cancelled(p.Allocations[0].OrderID, p.CheckoutID, "VNPAY")
			var ready, done sync.WaitGroup
			start := make(chan struct{})
			errs := make([]error, 2)
			ready.Add(2)
			done.Add(2)
			for n := 0; n < 2; n++ {
				go func() {
					defer done.Done()
					ready.Done()
					<-start
					if n == 0 {
						_, errs[n] = e.obs.ApplyObserved(ctx, "fake", id(), p.ID, success(p))
						return
					}
					errs[n] = rf.OrderCancelled(ctx, id(), ev, t0)
				}()
			}
			ready.Wait()
			close(start)
			done.Wait()
			require.NoError(t, errs[0])
			require.NoError(t, errs[1])
			assert.Len(t, e.refunds(t, p.ID), 1, "iteration %d", i)
		}
	})
}

func TestBackoff(t *testing.T) {
	for attempts, want := range map[int]time.Duration{
		0: 10 * time.Second, 1: 10 * time.Second, 2: 20 * time.Second, 3: 40 * time.Second,
		4: 80 * time.Second, 5: 160 * time.Second, 6: 320 * time.Second, 7: 10 * time.Minute,
		8: 10 * time.Minute, 100: 10 * time.Minute,
	} {
		assert.Equal(t, want, app.Backoff(attempts), "attempts=%d", attempts)
	}
}

type workerEnv struct {
	env
	clk *clock
	fp  *fake.Provider
	rf  *app.Refunds
	w   *app.RefundWorker
}

func newWorkerEnv(t *testing.T) workerEnv {
	e := newEnv(t)
	clk := &clock{t: t0.Add(time.Hour)}
	fp := fake.New(e.pool, fake.Config{})
	return workerEnv{env: e, clk: clk, fp: fp, rf: app.NewRefunds(e.store, clk.now),
		w: app.NewRefundWorker(e.store, app.NewProviders(fp, fp), 3, clk.now, quiet)}
}

// captured inserts a fake-provider payment with a session, captures it and cancels its first order.
func (e workerEnv) captured(t *testing.T, hooks fake.Hooks) app.NewPayment {
	p := newPayment(id(), domain.StatusPending)
	e.insert(t, p)
	_, err := e.fp.CreateSession(ctx, app.SessionInput{PaymentID: p.ID})
	require.NoError(t, err)
	if hooks != (fake.Hooks{}) {
		require.NoError(t, e.fp.ArmHooks(ctx, p.ID, hooks))
	}
	_, err = e.obs.ApplyObserved(ctx, "fake", id(), p.ID, success(p))
	require.NoError(t, err)
	require.NoError(t, e.rf.OrderCancelled(ctx, id(), cancelled(p.Allocations[0].OrderID, p.CheckoutID, "VNPAY"), t0))
	return p
}

func eventsOf(rows []outboxRow, typ string) []outboxRow {
	var out []outboxRow
	for _, r := range rows {
		if r.Type == typ {
			out = append(out, r)
		}
	}
	return out
}

func TestRefundWorkerFake(t *testing.T) {
	e := newWorkerEnv(t)

	t.Run("a refund succeeds and publishes PaymentRefunded once", func(t *testing.T) {
		p := e.captured(t, fake.Hooks{})
		n, err := e.w.RunOnce(ctx)
		require.NoError(t, err)
		assert.Equal(t, 1, n)
		rs := e.refunds(t, p.ID)
		require.Len(t, rs, 1)
		assert.Equal(t, "SUCCEEDED", rs[0].Status)
		evs := eventsOf(e.outbox(t, p.CheckoutID), "PaymentRefunded")
		require.Len(t, evs, 1)
		validate(t, "payment-refunded.schema.json", evs[0].Raw)
		n, err = e.w.RunOnce(ctx)
		require.NoError(t, err)
		assert.Equal(t, 0, n)
		assert.Len(t, eventsOf(e.outbox(t, p.CheckoutID), "PaymentRefunded"), 1)
	})

	t.Run("a duplicate payment refund carries a null order_id", func(t *testing.T) {
		co := id()
		a, b := newPayment(co, domain.StatusFailed), newPayment(co, domain.StatusPending)
		e.insert(t, a, b)
		for _, p := range []app.NewPayment{a, b} {
			_, err := e.fp.CreateSession(ctx, app.SessionInput{PaymentID: p.ID})
			require.NoError(t, err)
			_, err = e.obs.ApplyObserved(ctx, "fake", "k-"+p.ID, p.ID, success(p))
			require.NoError(t, err)
		}
		_, err := e.w.RunOnce(ctx)
		require.NoError(t, err)
		evs := eventsOf(e.outbox(t, co), "PaymentRefunded")
		require.Len(t, evs, 1)
		validate(t, "payment-refunded.schema.json", evs[0].Raw)
		var env struct {
			Data map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal(evs[0].Raw, &env))
		v, ok := env.Data["order_id"]
		assert.True(t, ok)
		assert.Nil(t, v)
	})

	t.Run("hook_refund=fail retries with backoff then FAILED with one RefundFailed", func(t *testing.T) {
		p := e.captured(t, fake.Hooks{Refund: "fail"})
		for run := 1; run <= 3; run++ {
			n, err := e.w.RunOnce(ctx)
			require.NoError(t, err)
			assert.Equal(t, 1, n, "run %d", run)
			if run < 3 {
				rs := e.refunds(t, p.ID)
				assert.Equal(t, "PENDING", rs[0].Status)
				n, err := e.w.RunOnce(ctx)
				require.NoError(t, err)
				assert.Equal(t, 0, n, "not due before its backoff")
				e.clk.advance(time.Minute)
			}
		}
		rs := e.refunds(t, p.ID)
		assert.Equal(t, "FAILED", rs[0].Status)
		evs := eventsOf(e.outbox(t, p.CheckoutID), "RefundFailed")
		require.Len(t, evs, 1)
		validate(t, "refund-failed.schema.json", evs[0].Raw)
		assert.Empty(t, eventsOf(e.outbox(t, p.CheckoutID), "PaymentRefunded"))
		n, err := e.w.RunOnce(ctx)
		require.NoError(t, err)
		assert.Equal(t, 0, n)
	})
}

// scriptedProvider scripts refund answers on top of stubProvider.
type scriptedProvider struct {
	*stubProvider
	refundErr  error
	query      app.RefundObserved
	queryErr   error
	deadline   *time.Time
	queriedRef string
}

func (s *scriptedProvider) Refund(ctx context.Context, in app.RefundInput) (app.RefundObserved, error) {
	if d, ok := ctx.Deadline(); ok {
		s.deadline = &d
	}
	if s.refundErr != nil {
		return app.RefundObserved{}, s.refundErr
	}
	return s.stubProvider.Refund(ctx, in)
}

func (s *scriptedProvider) QueryRefund(_ context.Context, r app.Refund) (app.RefundObserved, error) {
	s.queriedRef = r.ProviderRefundRef
	return s.query, s.queryErr
}

func TestRefundWorkerProviderOutcomes(t *testing.T) {
	e := newEnv(t)
	clk := &clock{t: t0.Add(time.Hour)}
	rf := app.NewRefunds(e.store, clk.now)
	sp := &scriptedProvider{stubProvider: &stubProvider{name: "fake"}}
	w := app.NewRefundWorker(e.store, app.NewProviders(sp, sp), 3, clk.now, quiet)

	newCancelled := func(t *testing.T) app.NewPayment {
		p := newPayment(id(), domain.StatusPending)
		e.insert(t, p)
		_, err := e.obs.ApplyObserved(ctx, "fake", id(), p.ID, success(p))
		require.NoError(t, err)
		require.NoError(t, rf.OrderCancelled(ctx, id(), cancelled(p.Allocations[0].OrderID, p.CheckoutID, "VNPAY"), t0))
		return p
	}

	t.Run("unavailable provider counts an attempt and backs off", func(t *testing.T) {
		sp.refundErr = errors.Join(app.ErrProviderUnavailable, errors.New("boom"))
		p := newCancelled(t)
		_, err := w.RunOnce(ctx)
		require.NoError(t, err)
		var attempts int
		var next time.Time
		var lastErr *string
		require.NoError(t, e.pool.QueryRow(ctx, `SELECT attempts, next_attempt_at, last_error FROM refunds WHERE payment_id=$1`, p.ID).Scan(&attempts, &next, &lastErr))
		assert.Equal(t, 1, attempts)
		assert.True(t, next.Equal(clk.now().Add(10*time.Second)))
		require.NotNil(t, lastErr)
		sp.refundErr = nil
	})

	t.Run("pending is rechecked and a hint settles it by ref or by refund id", func(t *testing.T) {
		// Drain the refund left over from the previous case so the ref below is unique.
		sp.refund = app.RefundObserved{State: app.RefundSucceeded, ProviderRefundRef: "re_drained"}
		clk.advance(time.Hour)
		_, err := w.RunOnce(ctx)
		require.NoError(t, err)

		sp.refund = app.RefundObserved{State: app.RefundPending, ProviderRefundRef: "re_1"}
		p := newCancelled(t)
		clk.advance(time.Hour)
		_, err = w.RunOnce(ctx)
		require.NoError(t, err)
		var status, ref, rid string
		require.NoError(t, e.pool.QueryRow(ctx, `SELECT status, provider_refund_ref, id::text FROM refunds WHERE payment_id=$1`, p.ID).Scan(&status, &ref, &rid))
		assert.Equal(t, "PENDING", status)
		assert.Equal(t, "re_1", ref)

		sp.query = app.RefundObserved{State: app.RefundSucceeded, ProviderRefundRef: "re_1"}
		require.NoError(t, w.ApplyRefundObserved(ctx, "fake", app.RefundHint{ProviderRefundRef: "re_1"}))
		assert.Equal(t, "SUCCEEDED", e.refunds(t, p.ID)[0].Status)
		assert.Len(t, eventsOf(e.outbox(t, p.CheckoutID), "PaymentRefunded"), 1)
		// A repeat hint is a no-op.
		require.NoError(t, w.ApplyRefundObserved(ctx, "fake", app.RefundHint{ProviderRefundRef: "re_1"}))
		assert.Len(t, eventsOf(e.outbox(t, p.CheckoutID), "PaymentRefunded"), 1)

		// Fallback to the refund id when the ref is unknown.
		sp.refund = app.RefundObserved{State: app.RefundPending}
		p2 := newCancelled(t)
		clk.advance(time.Hour)
		_, err = w.RunOnce(ctx)
		require.NoError(t, err)
		require.NoError(t, e.pool.QueryRow(ctx, `SELECT id::text FROM refunds WHERE payment_id=$1`, p2.ID).Scan(&rid))
		require.NoError(t, w.ApplyRefundObserved(ctx, "fake", app.RefundHint{ProviderRefundRef: "re_unseen", RefundID: rid}))
		assert.Equal(t, "SUCCEEDED", e.refunds(t, p2.ID)[0].Status)
	})

	t.Run("an unknown refund is ignored", func(t *testing.T) {
		assert.NoError(t, w.ApplyRefundObserved(ctx, "fake", app.RefundHint{ProviderRefundRef: "re_nope"}))
		assert.NoError(t, w.ApplyRefundObserved(ctx, "fake", app.RefundHint{ProviderRefundRef: "re_nope", RefundID: id()}))
		assert.NoError(t, w.ApplyRefundObserved(ctx, "fake", app.RefundHint{RefundID: "not-a-uuid"}))
	})
}

func findPayment(rows []domain.Payment, id string) (domain.Payment, bool) {
	for _, r := range rows {
		if r.ID == id {
			return r, true
		}
	}
	return domain.Payment{}, false
}

func TestPendingBackoff(t *testing.T) {
	for age, want := range map[time.Duration]time.Duration{
		-time.Minute: 10 * time.Second, 0: 10 * time.Second, 10 * time.Second: 10 * time.Second,
		time.Minute: 30 * time.Second, 10 * time.Minute: 5 * time.Minute,
		20 * time.Minute: 10 * time.Minute, 3 * time.Hour: 10 * time.Minute, 48 * time.Hour: 10 * time.Minute,
	} {
		assert.Equal(t, want, app.PendingBackoff(age), "age=%s", age)
	}
}

// failingStore fails the failOn-th UpdateRefund, simulating a commit failure after the provider call.
type failingStore struct {
	app.Store
	failOn int32
	n      atomic.Int32
}

func (f *failingStore) InTx(ctx context.Context, fn func(app.Tx) error) error {
	return f.Store.InTx(ctx, func(tx app.Tx) error { return fn(&failingTx{Tx: tx, f: f}) })
}

type failingTx struct {
	app.Tx
	f *failingStore
}

func (t *failingTx) UpdateRefund(ctx context.Context, r app.Refund) error {
	if t.f.n.Add(1) == t.f.failOn {
		return errors.New("injected commit failure")
	}
	return t.Tx.UpdateRefund(ctx, r)
}

func (e workerEnv) refundedTotal(t *testing.T, paymentID string) int64 {
	var n int64
	require.NoError(t, e.pool.QueryRow(ctx, `SELECT refunded FROM fake_sessions WHERE payment_id=$1`, paymentID).Scan(&n))
	return n
}

func TestRefundWorkerTransactions(t *testing.T) {
	t.Run("a failed commit after the provider call is settled later without refunding twice", func(t *testing.T) {
		e := newWorkerEnv(t)
		fs := &failingStore{Store: e.store, failOn: 1}
		w := app.NewRefundWorker(fs, app.NewProviders(e.fp, e.fp), 3, e.clk.now, quiet)
		p := e.captured(t, fake.Hooks{})
		_, err := w.RunOnce(ctx)
		require.Error(t, err)
		assert.Equal(t, "PENDING", e.refunds(t, p.ID)[0].Status)
		assert.EqualValues(t, p.Allocations[0].Amount, e.refundedTotal(t, p.ID), "the provider already refunded")

		n, err := w.RunOnce(ctx)
		require.NoError(t, err)
		assert.Equal(t, 1, n)
		assert.Equal(t, "SUCCEEDED", e.refunds(t, p.ID)[0].Status)
		assert.EqualValues(t, p.Allocations[0].Amount, e.refundedTotal(t, p.ID), "refunded exactly once")
		assert.Len(t, eventsOf(e.outbox(t, p.CheckoutID), "PaymentRefunded"), 1)
	})

	t.Run("one refund's failure does not roll back the ones already settled", func(t *testing.T) {
		e := newWorkerEnv(t)
		fs := &failingStore{Store: e.store, failOn: 2}
		w := app.NewRefundWorker(fs, app.NewProviders(e.fp, e.fp), 3, e.clk.now, quiet)
		a, b := e.captured(t, fake.Hooks{}), e.captured(t, fake.Hooks{})
		_, err := w.RunOnce(ctx)
		require.Error(t, err)
		var done int
		for _, p := range []app.NewPayment{a, b} {
			if e.refunds(t, p.ID)[0].Status == "SUCCEEDED" {
				done++
			}
		}
		assert.Equal(t, 1, done)
	})

	t.Run("every provider call is bounded by a timeout", func(t *testing.T) {
		e := newEnv(t)
		clk := &clock{t: t0.Add(time.Hour)}
		rf := app.NewRefunds(e.store, clk.now)
		sp := &scriptedProvider{stubProvider: &stubProvider{name: "fake", refund: app.RefundObserved{State: app.RefundSucceeded}}}
		w := app.NewRefundWorker(e.store, app.NewProviders(sp, sp), 3, clk.now, quiet)
		p := newPayment(id(), domain.StatusPending)
		e.insert(t, p)
		_, err := e.obs.ApplyObserved(ctx, "fake", id(), p.ID, success(p))
		require.NoError(t, err)
		require.NoError(t, rf.OrderCancelled(ctx, id(), cancelled(p.Allocations[0].OrderID, p.CheckoutID, "VNPAY"), t0))
		_, err = w.RunOnce(ctx)
		require.NoError(t, err)
		require.NotNil(t, sp.deadline, "Refund got a context without a deadline")
		assert.LessOrEqual(t, time.Until(*sp.deadline), 15*time.Second)
	})
}

// pendingEnv is a worker over a scriptable provider with a captured log.
type pendingEnv struct {
	env
	clk *clock
	rf  *app.Refunds
	sp  *scriptedProvider
	w   *app.RefundWorker
	log *bytes.Buffer
}

func newPendingEnv(t *testing.T) pendingEnv {
	e := newEnv(t)
	clk := &clock{t: t0.Add(time.Hour)}
	sp := &scriptedProvider{stubProvider: &stubProvider{name: "fake"}}
	buf := &bytes.Buffer{}
	return pendingEnv{env: e, clk: clk, rf: app.NewRefunds(e.store, clk.now), sp: sp, log: buf,
		w: app.NewRefundWorker(e.store, app.NewProviders(sp, sp), 3, clk.now, slog.New(slog.NewTextHandler(buf, nil)))}
}

// refund makes a due refund; ref/age shape its state (ref "" = the provider has not accepted it).
func (e pendingEnv) refund(t *testing.T, ref string, age time.Duration) (paymentID, refundID string, checkout string) {
	p := newPayment(id(), domain.StatusPending)
	e.insert(t, p)
	_, err := e.obs.ApplyObserved(ctx, "fake", id(), p.ID, success(p))
	require.NoError(t, err)
	require.NoError(t, e.rf.OrderCancelled(ctx, id(), cancelled(p.Allocations[0].OrderID, p.CheckoutID, "VNPAY"), t0))
	_, err = e.pool.Exec(ctx, `UPDATE refunds SET created_at = $2, provider_refund_ref = NULLIF($3, '') WHERE payment_id = $1`,
		p.ID, e.clk.now().Add(-age), ref)
	require.NoError(t, err)
	require.NoError(t, e.pool.QueryRow(ctx, `SELECT id::text FROM refunds WHERE payment_id=$1`, p.ID).Scan(&refundID))
	return p.ID, refundID, p.CheckoutID
}

type refundRow struct {
	Status, Ref string
	Attempts    int
	Next        time.Time
}

func (e pendingEnv) row(t *testing.T, paymentID string) refundRow {
	var r refundRow
	var ref *string
	require.NoError(t, e.pool.QueryRow(ctx, `SELECT status, provider_refund_ref, attempts, next_attempt_at FROM refunds WHERE payment_id=$1`, paymentID).
		Scan(&r.Status, &ref, &r.Attempts, &r.Next))
	if ref != nil {
		r.Ref = *ref
	}
	return r
}

func TestRefundWorkerPending(t *testing.T) {
	t.Run("pending polls back off by the refund's age", func(t *testing.T) {
		e := newPendingEnv(t)
		e.sp.refund = app.RefundObserved{State: app.RefundPending, ProviderRefundRef: "re_p"}
		for _, age := range []time.Duration{0, time.Minute, 10 * time.Minute, 3 * time.Hour} {
			pid, _, _ := e.refund(t, "", age)
			_, err := e.w.RunOnce(ctx)
			require.NoError(t, err)
			r := e.row(t, pid)
			assert.Equal(t, "PENDING", r.Status)
			assert.Equal(t, 0, r.Attempts, "pending is not a failed attempt")
			assert.True(t, r.Next.Equal(e.clk.now().Add(app.PendingBackoff(age))), "age %s: next %s", age, r.Next.Sub(e.clk.now()))
			// A later poll (QueryRefund) reschedules the same way.
			e.sp.query = app.RefundObserved{State: app.RefundPending, ProviderRefundRef: "re_p"}
			e.clk.advance(app.PendingBackoff(age))
			_, err = e.w.RunOnce(ctx)
			require.NoError(t, err)
			e.clk.advance(-app.PendingBackoff(age))
		}
	})

	t.Run("still pending after 24h logs an error and never fails", func(t *testing.T) {
		e := newPendingEnv(t)
		e.sp.query = app.RefundObserved{State: app.RefundPending, ProviderRefundRef: "re_old"}
		pid, _, _ := e.refund(t, "re_old", 25*time.Hour)
		for i := 0; i < 5; i++ {
			_, err := e.w.RunOnce(ctx)
			require.NoError(t, err)
			e.clk.advance(time.Hour)
		}
		assert.Equal(t, "PENDING", e.row(t, pid).Status)
		assert.Contains(t, e.log.String(), "level=ERROR")
		assert.Contains(t, e.log.String(), "refund still pending")
	})

	t.Run("a young pending refund does not log an error", func(t *testing.T) {
		e := newPendingEnv(t)
		e.sp.query = app.RefundObserved{State: app.RefundPending, ProviderRefundRef: "re_new"}
		e.refund(t, "re_new", time.Minute)
		_, err := e.w.RunOnce(ctx)
		require.NoError(t, err)
		assert.NotContains(t, e.log.String(), "level=ERROR")
	})
}

func TestRefundWorkerAccepted(t *testing.T) {
	t.Run("unavailable on an accepted refund is not an attempt", func(t *testing.T) {
		e := newPendingEnv(t)
		e.sp.queryErr = app.ErrProviderUnavailable
		pid, _, _ := e.refund(t, "re_acc", 2*time.Minute)
		for i := 0; i < 6; i++ { // more polls than maxAttempts
			_, err := e.w.RunOnce(ctx)
			require.NoError(t, err)
			r := e.row(t, pid)
			assert.Equal(t, "PENDING", r.Status)
			assert.Equal(t, 0, r.Attempts)
			assert.True(t, r.Next.After(e.clk.now()))
			e.clk.advance(11 * time.Minute)
		}
		assert.Empty(t, eventsOf(e.outbox(t, mustCheckout(t, e.env, pid)), "RefundFailed"))
	})

	t.Run("a provider-reported failure on an accepted refund fails it at once", func(t *testing.T) {
		e := newPendingEnv(t)
		e.sp.query = app.RefundObserved{State: app.RefundFailed, Reason: "declined"}
		pid, _, co := e.refund(t, "re_acc", time.Minute)
		_, err := e.w.RunOnce(ctx)
		require.NoError(t, err)
		assert.Equal(t, "FAILED", e.row(t, pid).Status)
		evs := eventsOf(e.outbox(t, co), "RefundFailed")
		require.Len(t, evs, 1)
		validate(t, "refund-failed.schema.json", evs[0].Raw)
	})

	t.Run("without a ref, failed and unavailable both count toward the limit", func(t *testing.T) {
		e := newPendingEnv(t)
		e.sp.refundErr = app.ErrProviderUnavailable
		pid, _, _ := e.refund(t, "", time.Minute)
		_, err := e.w.RunOnce(ctx)
		require.NoError(t, err)
		assert.Equal(t, 1, e.row(t, pid).Attempts)
		e.sp.refundErr = nil
		e.sp.refund = app.RefundObserved{State: app.RefundFailed, Reason: "no"}
		e.clk.advance(time.Minute)
		_, err = e.w.RunOnce(ctx)
		require.NoError(t, err)
		assert.Equal(t, 2, e.row(t, pid).Attempts)
	})
}

func mustCheckout(t *testing.T, e env, paymentID string) string {
	var co string
	require.NoError(t, e.pool.QueryRow(ctx, `SELECT checkout_id::text FROM payments WHERE id=$1`, paymentID).Scan(&co))
	return co
}

func TestRefundWorkerHint(t *testing.T) {
	t.Run("provider unavailable is returned and consumes no attempt", func(t *testing.T) {
		e := newPendingEnv(t)
		e.sp.queryErr = app.ErrProviderUnavailable
		pid, _, _ := e.refund(t, "re_h", time.Minute)
		before := e.row(t, pid)
		err := e.w.ApplyRefundObserved(ctx, "fake", app.RefundHint{ProviderRefundRef: "re_h"})
		require.ErrorIs(t, err, app.ErrProviderUnavailable)
		after := e.row(t, pid)
		assert.Equal(t, before, after)
	})

	t.Run("the hint adopts the signed ref when the row has none", func(t *testing.T) {
		e := newPendingEnv(t)
		e.sp.query = app.RefundObserved{State: app.RefundPending}
		pid, rid, _ := e.refund(t, "", time.Minute)
		require.NoError(t, e.w.ApplyRefundObserved(ctx, "fake", app.RefundHint{ProviderRefundRef: "re_signed", RefundID: rid}))
		r := e.row(t, pid)
		assert.Equal(t, "re_signed", r.Ref)
		assert.Equal(t, "PENDING", r.Status)
		assert.Equal(t, "re_signed", e.sp.queriedRef, "QueryRefund is called with the adopted ref")
	})

	t.Run("a hint for another provider adopts nothing", func(t *testing.T) {
		e := newPendingEnv(t)
		pid, rid, _ := e.refund(t, "", time.Minute)
		require.NoError(t, e.w.ApplyRefundObserved(ctx, "stripe", app.RefundHint{ProviderRefundRef: "re_x", RefundID: rid}))
		assert.Equal(t, "", e.row(t, pid).Ref)
	})
}

func TestRefundWorkerMissingAdapter(t *testing.T) {
	t.Run("an accepted refund waits and is never an attempt", func(t *testing.T) {
		e := newPendingEnv(t)
		pid, _, co := e.refund(t, "re_acc", 2*time.Minute)
		// The config now serves only "vnpay"; the refund row belongs to "fake".
		other := &scriptedProvider{stubProvider: &stubProvider{name: "vnpay"}}
		w := app.NewRefundWorker(e.store, app.NewProviders(other, other), 3, e.clk.now, slog.New(slog.NewTextHandler(e.log, nil)))
		for i := 0; i < 6; i++ { // more polls than maxAttempts
			_, err := w.RunOnce(ctx)
			require.NoError(t, err)
			r := e.row(t, pid)
			assert.Equal(t, "PENDING", r.Status)
			assert.Equal(t, 0, r.Attempts)
			assert.True(t, r.Next.After(e.clk.now()))
			e.clk.advance(11 * time.Minute)
		}
		assert.Empty(t, eventsOf(e.outbox(t, co), "RefundFailed"))
		assert.Contains(t, e.log.String(), "level=ERROR")
		assert.Contains(t, e.log.String(), "has no adapter configured")
	})
}
