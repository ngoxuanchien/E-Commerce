package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
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

	t.Run("the cancellation waits for the checkout's payment lock", func(t *testing.T) {
		p := newPayment(id(), domain.StatusPending)
		e.insert(t, p)
		ev := cancelled(p.Allocations[0].OrderID, p.CheckoutID, "VNPAY")
		release, locked, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		var once sync.Once
		unlock := func() { once.Do(func() { close(release) }) }
		defer unlock()
		holder := make(chan error, 1)
		go func() {
			holder <- e.store.InTx(ctx, func(tx app.Tx) error {
				if _, err := tx.LockCheckoutPayments(ctx, p.CheckoutID); err != nil {
					return err
				}
				close(locked)
				<-release
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
	refundErr error
	query     app.RefundObserved
}

func (s *scriptedProvider) Refund(ctx context.Context, in app.RefundInput) (app.RefundObserved, error) {
	if s.refundErr != nil {
		return app.RefundObserved{}, s.refundErr
	}
	return s.stubProvider.Refund(ctx, in)
}

func (s *scriptedProvider) QueryRefund(context.Context, app.Refund) (app.RefundObserved, error) {
	return s.query, nil
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
