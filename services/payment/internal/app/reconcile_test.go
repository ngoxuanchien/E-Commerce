package app_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	paymentv1 "github.com/ngoxuanchien/e-commerce/gen/payment/v1"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/fake"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/domain"
)

func (c *clock) set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

// cancelEnv wires the real FAKE adapter, an Observer and the Reconciler/Canceller on one clock.
type cancelEnv struct {
	env
	clk  *clock
	fake *fake.Provider
	rec  *app.Reconciler
	can  *app.Canceller
}

func newCancelEnv(t *testing.T, other app.Provider) cancelEnv {
	t.Helper()
	e := newEnv(t)
	clk := &clock{t: t0}
	f := fake.New(e.pool, fake.Config{PublicBaseURL: "http://gw.test", Now: clk.now, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	t.Cleanup(f.Wait)
	provs := app.NewProviders(f, other)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	obs := app.NewObserver(e.store, clk.now, log)
	return cancelEnv{
		env: e, clk: clk, fake: f,
		rec: app.NewReconciler(e.store, provs, obs, app.ReconcileConfig{Interval: time.Minute, MinAge: 2 * time.Minute}, clk.now, log),
		can: app.NewCanceller(e.store, provs, obs, clk.now),
	}
}

// fakePayment inserts a PENDING FAKE payment with a session, expiring 15 min after t0.
func (c cancelEnv) fakePayment(t *testing.T) app.NewPayment {
	t.Helper()
	p := newPayment(id(), domain.StatusPending)
	c.insert(t, p)
	_, err := c.fake.CreateSession(ctx, app.SessionInput{PaymentID: p.ID, CheckoutID: p.CheckoutID, Amount: p.Amount, ExpiresAt: p.ExpiresAt})
	require.NoError(t, err)
	return p
}

func (c cancelEnv) status(t *testing.T, id string) domain.Payment {
	t.Helper()
	p, err := c.store.GetPayment(ctx, id)
	require.NoError(t, err)
	return p
}

func TestReconcilerRunOnce(t *testing.T) {
	c := newCancelEnv(t, nil)

	t.Run("a dropped webhook is repaired by polling", func(t *testing.T) {
		p := c.fakePayment(t)
		require.NoError(t, c.fake.Submit(ctx, p.ID, "succeed", fake.Hooks{Webhook: "drop"}))
		c.fake.Wait()
		assert.Equal(t, domain.StatusPending, c.status(t, p.ID).Status)

		c.clk.set(t0.Add(3 * time.Minute)) // MinAge passed
		require.NoError(t, c.rec.RunOnce(ctx))
		got := c.status(t, p.ID)
		assert.Equal(t, domain.StatusSucceeded, got.Status)
		assert.True(t, got.Captured)
		assert.Equal(t, 1, c.count(t, `SELECT count(*) FROM outbox WHERE msg_key = $1`, p.CheckoutID))

		require.NoError(t, c.rec.RunOnce(ctx)) // idempotent
		assert.Equal(t, 1, c.count(t, `SELECT count(*) FROM outbox WHERE msg_key = $1`, p.CheckoutID))
	})

	t.Run("a payment younger than MinAge is left alone", func(t *testing.T) {
		c.clk.set(t0)
		p := newPayment(id(), domain.StatusPending)
		p.CreatedAt = t0
		c.insert(t, p)
		_, err := c.fake.CreateSession(ctx, app.SessionInput{PaymentID: p.ID})
		require.NoError(t, err)
		require.NoError(t, c.fake.Submit(ctx, p.ID, "succeed", fake.Hooks{Webhook: "drop"}))
		c.clk.set(t0.Add(time.Minute))
		require.NoError(t, c.rec.RunOnce(ctx))
		assert.Equal(t, domain.StatusPending, c.status(t, p.ID).Status)
	})

	t.Run("a stuck PENDING past expires_at + 5 min is EXPIRED", func(t *testing.T) {
		c.clk.set(t0)
		p := c.fakePayment(t)
		c.fake.Wait()
		c.clk.set(t0.Add(15*time.Minute + 6*time.Minute))
		require.NoError(t, c.rec.RunOnce(ctx))
		assert.Equal(t, domain.StatusExpired, c.status(t, p.ID).Status)
	})

	t.Run("a slow Query leaves Cancel its own provider budget", func(t *testing.T) {
		c.clk.set(t0)
		const budget = 400 * time.Millisecond
		slow := &slowQueryProvider{
			stubProvider: stubProvider{name: "stripe", observed: domain.Observed{State: domain.ObservedPending}},
			delay:        3 * budget / 4, minLeft: budget / 2,
		}
		p := newPayment(id(), domain.StatusPending)
		p.Provider = "stripe"
		p.ExpiresAt = t0.Add(15 * time.Minute)
		c.insert(t, p)
		rec := app.NewReconciler(c.store, app.NewProviders(c.fake, slow), app.NewObserver(c.store, c.clk.now, slog.Default()),
			app.ReconcileConfig{MinAge: time.Minute, ProviderTimeout: budget}, c.clk.now, slog.New(slog.NewTextHandler(io.Discard, nil)))
		c.clk.set(p.ExpiresAt.Add(6 * time.Minute))
		require.NoError(t, rec.RunOnce(ctx))
		assert.Equal(t, 1, slow.cancels)
		assert.Equal(t, domain.StatusExpired, c.status(t, p.ID).Status)
	})

	t.Run("past expires_at but inside the 5 min grace stays PENDING", func(t *testing.T) {
		c.clk.set(t0)
		p := c.fakePayment(t)
		require.NoError(t, c.fake.ArmHooks(ctx, p.ID, fake.Hooks{Cancel: "unsupported"}))
		stub := &stubProvider{name: "stripe", observed: domain.Observed{State: domain.ObservedPending}}
		g := newPayment(id(), domain.StatusPending)
		g.Provider = "stripe"
		g.ExpiresAt = t0.Add(15 * time.Minute)
		c.insert(t, g)
		rec := app.NewReconciler(c.store, app.NewProviders(c.fake, stub), app.NewObserver(c.store, c.clk.now, slog.Default()),
			app.ReconcileConfig{MinAge: time.Minute}, c.clk.now, slog.New(slog.NewTextHandler(io.Discard, nil)))
		c.clk.set(g.ExpiresAt.Add(3 * time.Minute))
		require.NoError(t, rec.RunOnce(ctx))
		assert.Equal(t, domain.StatusPending, c.status(t, g.ID).Status)
		assert.Equal(t, 0, stub.cancels)
		c.clk.set(g.ExpiresAt.Add(6 * time.Minute))
		require.NoError(t, rec.RunOnce(ctx))
		assert.Equal(t, 1, stub.cancels)
	})

	t.Run("Run survives a non-positive interval", func(t *testing.T) {
		rec := app.NewReconciler(c.store, app.NewProviders(c.fake, nil), app.NewObserver(c.store, c.clk.now, slog.Default()),
			app.ReconcileConfig{Interval: 0, MinAge: time.Minute}, c.clk.now, slog.New(slog.NewTextHandler(io.Discard, nil)))
		rctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
		defer cancel()
		assert.NotPanics(t, func() { assert.ErrorIs(t, rec.Run(rctx), context.DeadlineExceeded) })
	})

	t.Run("a late success past the grace still wins over expiry", func(t *testing.T) {
		c.clk.set(t0)
		p := c.fakePayment(t)
		require.NoError(t, c.fake.Submit(ctx, p.ID, "succeed", fake.Hooks{Webhook: "drop"}))
		c.fake.Wait()
		c.clk.set(t0.Add(30 * time.Minute))
		require.NoError(t, c.rec.RunOnce(ctx))
		assert.Equal(t, domain.StatusSucceeded, c.status(t, p.ID).Status)
	})

	t.Run("a provider error skips that payment and does not abort the run", func(t *testing.T) {
		c.clk.set(t0)
		bad := newPayment(id(), domain.StatusPending)
		bad.Provider = "stripe"
		bad.CreatedAt = t0.Add(-time.Hour)
		bad.ExpiresAt = t0.Add(-time.Hour)
		c.insert(t, bad)
		good := c.fakePayment(t)
		c.fake.Wait()
		stub := &stubProvider{name: "stripe", queryErr: app.ErrProviderUnavailable}
		c2 := app.NewReconciler(c.store, app.NewProviders(c.fake, stub), app.NewObserver(c.store, c.clk.now, slog.Default()),
			app.ReconcileConfig{MinAge: time.Minute}, c.clk.now, slog.New(slog.NewTextHandler(io.Discard, nil)))
		require.NoError(t, c.fake.Submit(ctx, good.ID, "succeed", fake.Hooks{Webhook: "drop"}))
		c.clk.set(t0.Add(3 * time.Minute))
		require.NoError(t, c2.RunOnce(ctx))
		assert.Equal(t, domain.StatusPending, c.status(t, bad.ID).Status)
		assert.Equal(t, domain.StatusSucceeded, c.status(t, good.ID).Status)
	})

	t.Run("purges delete old rows and keep fresh ones", func(t *testing.T) {
		c.clk.set(t0.Add(48 * time.Hour))
		uid := id()
		for k, at := range map[string]time.Time{"old": c.clk.now().Add(-25 * time.Hour), "fresh": c.clk.now().Add(-time.Hour)} {
			_, err := c.pool.Exec(ctx, `INSERT INTO idempotency_keys (scope, user_id, key, request_hash, created_at) VALUES ('s',$1,$2,'\x00',$3)`, uid, k, at)
			require.NoError(t, err)
		}
		for k, at := range map[string]time.Time{"old": c.clk.now().Add(-31 * 24 * time.Hour), "fresh": c.clk.now().Add(-29 * 24 * time.Hour)} {
			_, err := c.pool.Exec(ctx, `INSERT INTO provider_events (provider, event_key, received_at) VALUES ('purge-test',$1,$2)`, k, at)
			require.NoError(t, err)
		}
		require.NoError(t, c.rec.RunOnce(ctx))
		assert.Equal(t, 1, c.count(t, `SELECT count(*) FROM idempotency_keys WHERE user_id = $1 AND key = 'fresh'`, uid))
		assert.Equal(t, 0, c.count(t, `SELECT count(*) FROM idempotency_keys WHERE user_id = $1 AND key = 'old'`, uid))
		assert.Equal(t, 1, c.count(t, `SELECT count(*) FROM provider_events WHERE provider = 'purge-test' AND event_key = 'fresh'`))
		assert.Equal(t, 0, c.count(t, `SELECT count(*) FROM provider_events WHERE provider = 'purge-test' AND event_key = 'old'`))
	})
}

func TestCancelCheckoutPayments(t *testing.T) {
	t.Run("FAKE cancels and the attempt expires: NOT_PAID", func(t *testing.T) {
		c := newCancelEnv(t, nil)
		p := c.fakePayment(t)
		out, err := c.can.CancelCheckoutPayments(ctx, p.CheckoutID)
		require.NoError(t, err)
		assert.Equal(t, paymentv1.Outcome_OUTCOME_NOT_PAID, out)
		assert.Equal(t, domain.StatusExpired, c.status(t, p.ID).Status)
	})

	t.Run("a captured attempt: PAID", func(t *testing.T) {
		c := newCancelEnv(t, nil)
		p := c.fakePayment(t)
		require.NoError(t, c.fake.Submit(ctx, p.ID, "succeed", fake.Hooks{Webhook: "drop"}))
		c.fake.Wait()
		out, err := c.can.CancelCheckoutPayments(ctx, p.CheckoutID)
		require.NoError(t, err)
		assert.Equal(t, paymentv1.Outcome_OUTCOME_PAID, out)
		assert.True(t, c.status(t, p.ID).Captured)
	})

	t.Run("an EXPIRED row captured by a late success plus a PENDING row: PAID", func(t *testing.T) {
		c := newCancelEnv(t, nil)
		checkout := id()
		old := newPayment(checkout, domain.StatusExpired)
		c.insert(t, old)
		_, err := c.obs.ApplyObserved(ctx, "fake", "late", old.ID, success(old))
		require.NoError(t, err)
		require.True(t, c.status(t, old.ID).Captured)
		open := newPayment(checkout, domain.StatusPending)
		c.insert(t, open)
		_, err = c.fake.CreateSession(ctx, app.SessionInput{PaymentID: open.ID})
		require.NoError(t, err)
		out, err := c.can.CancelCheckoutPayments(ctx, checkout)
		require.NoError(t, err)
		assert.Equal(t, paymentv1.Outcome_OUTCOME_PAID, out)
		assert.Equal(t, domain.StatusExpired, c.status(t, open.ID).Status)
	})

	t.Run("an unknown checkout: NOT_PAID", func(t *testing.T) {
		c := newCancelEnv(t, nil)
		out, err := c.can.CancelCheckoutPayments(ctx, id())
		require.NoError(t, err)
		assert.Equal(t, paymentv1.Outcome_OUTCOME_NOT_PAID, out)
	})

	t.Run("a provider error: ErrProviderUnavailable", func(t *testing.T) {
		stub := &stubProvider{name: "stripe", queryErr: app.ErrProviderUnavailable}
		c := newCancelEnv(t, stub)
		p := newPayment(id(), domain.StatusPending)
		p.Provider = "stripe"
		c.insert(t, p)
		_, err := c.can.CancelCheckoutPayments(ctx, p.CheckoutID)
		assert.True(t, errors.Is(err, app.ErrProviderUnavailable), "%v", err)
		assert.Equal(t, domain.StatusPending, c.status(t, p.ID).Status)
	})

	t.Run("cancel unsupported: still payable before expires_at, EXPIRED after", func(t *testing.T) {
		c := newCancelEnv(t, nil)
		p := c.fakePayment(t)
		require.NoError(t, c.fake.ArmHooks(ctx, p.ID, fake.Hooks{Cancel: "unsupported"}))

		c.clk.set(t0.Add(5 * time.Minute))
		_, err := c.can.CancelCheckoutPayments(ctx, p.CheckoutID)
		assert.ErrorIs(t, err, app.ErrStillPayable)
		assert.Equal(t, domain.StatusPending, c.status(t, p.ID).Status)

		c.clk.set(p.ExpiresAt)
		out, err := c.can.CancelCheckoutPayments(ctx, p.CheckoutID)
		require.NoError(t, err)
		assert.Equal(t, paymentv1.Outcome_OUTCOME_NOT_PAID, out)
		assert.Equal(t, domain.StatusExpired, c.status(t, p.ID).Status)
	})
}

// slowQueryProvider spends delay on its first Query and refuses a Cancel whose context has less than
// minLeft before its deadline, the way a real HTTP call would time out.
type slowQueryProvider struct {
	stubProvider
	delay, minLeft time.Duration
	queried        bool
}

func (s *slowQueryProvider) Query(ctx context.Context, p domain.Payment) (domain.Observed, error) {
	s.mu.Lock()
	first := !s.queried
	s.queried = true
	s.mu.Unlock()
	if first {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return domain.Observed{}, ctx.Err()
		}
	}
	return s.stubProvider.Query(ctx, p)
}

func (s *slowQueryProvider) Cancel(ctx context.Context, p domain.Payment) (bool, error) {
	if dl, ok := ctx.Deadline(); !ok || time.Until(dl) < s.minLeft {
		return false, context.DeadlineExceeded
	}
	return s.stubProvider.Cancel(ctx, p)
}
