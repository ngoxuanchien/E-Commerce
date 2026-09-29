package app_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	catalogv1 "github.com/ngoxuanchien/e-commerce/gen/catalog/v1"
	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/catalog/catalogfake"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

// crashedCheckout stores a PENDING order created at `at` holding a stock
// reservation, as if the service died between Tx A and Tx C.
func crashedCheckout(t *testing.T, e *env, at time.Time, stock int32) (domain.Order, *catalogv1.SkuInfo) {
	t.Helper()
	s := e.fake.AddSku(catalogfake.NewShop("Crash"), 10_000, stock)
	skus, err := e.catalog.GetSkus(ctx, []string{s.GetSkuId()})
	require.NoError(t, err)
	orders, err := domain.BuildOrders(domain.CheckoutParams{
		CheckoutID: uid(), BuyerID: uid(), Lines: []domain.Line{{SkuID: s.GetSkuId(), Quantity: 2}}, Skus: skus,
		ShippingFee: 30_000, Address: testAddress, PaymentMethod: domain.PaymentCOD, Now: at, NewID: uid,
	})
	require.NoError(t, err)
	o := orders[0]
	require.NoError(t, e.store.InTx(ctx, func(tx app.Tx) error { return tx.InsertOrder(ctx, o) }))
	require.NoError(t, e.catalog.ReserveStock(ctx, o.ID, o.StockLines()))
	return o, s
}

// sweepDuringReserve runs `run` once, just before the first reservation.
type sweepDuringReserve struct {
	app.Catalog
	once sync.Once
	run  func()
}

func (c *sweepDuringReserve) ReserveStock(ctx context.Context, orderID string, lines []domain.Line) error {
	c.once.Do(c.run)
	return c.Catalog.ReserveStock(ctx, orderID, lines)
}

func TestSweeper(t *testing.T) {
	e := newEnv(t, envOpts{postgres: true, redis: true})
	sw := app.NewSweeper(e.store, e.catalog, app.SweeperConfig{
		Interval: time.Minute, PendingTimeout: 2 * time.Minute, BatchSize: 2, Now: e.clock.Now,
	})

	t.Run("stale pending orders are cancelled and released", func(t *testing.T) {
		o, s := crashedCheckout(t, e, e.clock.Now(), 5)
		require.Equal(t, int32(3), e.fake.Stock(s.GetSkuId()))
		e.clock.Advance(3 * time.Minute)

		n, err := sw.RunOnce(ctx)
		require.NoError(t, err)
		assert.Equal(t, 1, n)
		got, err := e.store.GetOrder(ctx, o.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusCancelled, got.Status)
		assert.Equal(t, domain.CancelCheckoutTimeout, *got.CancelReason)
		assert.Equal(t, domain.RoleSystem, got.History[1].ActorRole)
		assert.Equal(t, int32(5), e.fake.Stock(s.GetSkuId()))
		assert.False(t, e.fake.Reserved(o.ID))

		evs := e.events(t, o.ID)
		require.Equal(t, []event.Type{event.TypeOrderStatusChanged, event.TypeOrderCancelled}, types(evs))
		c, err := event.DecodeData[event.OrderCancelled](evs[1])
		require.NoError(t, err)
		assert.Equal(t, event.OrderCancelled{OrderID: o.ID, Reason: "CHECKOUT_TIMEOUT", CancelledBy: "system"}, c)
	})

	t.Run("fresh pending and non-pending orders are left alone", func(t *testing.T) {
		old := e.seedOrder(t, uid(), catalogfake.NewShop("Old"), domain.StatusPendingConfirmation)
		e.clock.Advance(3 * time.Minute)
		fresh, _ := crashedCheckout(t, e, e.clock.Now(), 5)
		e.clock.Advance(time.Minute)

		n, err := sw.RunOnce(ctx)
		require.NoError(t, err)
		assert.Zero(t, n)
		for id, want := range map[string]domain.Status{old.ID: domain.StatusPendingConfirmation, fresh.ID: domain.StatusPending} {
			got, err := e.store.GetOrder(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, want, got.Status)
		}
		e.clock.Advance(2 * time.Minute) // let `fresh` go stale and clean it up
		_, err = sw.RunOnce(ctx)
		require.NoError(t, err)
	})

	t.Run("release failures do not block cancellation", func(t *testing.T) {
		o, _ := crashedCheckout(t, e, e.clock.Now(), 5)
		e.clock.Advance(3 * time.Minute)
		e.fake.FailNext(catalogfake.ReleaseStock, codes.Unavailable, 3)

		_, err := sw.RunOnce(ctx)
		require.NoError(t, err)
		got, err := e.store.GetOrder(ctx, o.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusCancelled, got.Status)
		assert.True(t, e.fake.Reserved(o.ID), "catalog will release it when it consumes OrderCancelled")
		assert.Contains(t, types(e.events(t, o.ID)), event.TypeOrderCancelled)
	})

	t.Run("works through every batch", func(t *testing.T) {
		for range 5 {
			crashedCheckout(t, e, e.clock.Now(), 5)
		}
		e.clock.Advance(3 * time.Minute)
		n, err := sw.RunOnce(ctx)
		require.NoError(t, err)
		assert.Equal(t, 5, n, "batch size 2 → 3 batches in one run")
	})

	t.Run("expired idempotency keys are purged", func(t *testing.T) {
		user, hash := uid(), []byte("h")
		_, _, err := e.store.BeginIdempotency(ctx, user, "k", hash, e.clock.Now(), time.Time{})
		require.NoError(t, err)
		e.clock.Advance(25 * time.Hour)
		_, err = sw.RunOnce(ctx)
		require.NoError(t, err)
		_, created, err := e.store.BeginIdempotency(ctx, user, "k", hash, e.clock.Now(), time.Time{})
		require.NoError(t, err)
		assert.True(t, created)
	})

	t.Run("a checkout that outlives PENDING_TIMEOUT loses to the sweeper", func(t *testing.T) {
		hook := &sweepDuringReserve{Catalog: e.catalog, run: func() {
			e.clock.Advance(3 * time.Minute)
			_, err := sw.RunOnce(ctx)
			assert.NoError(t, err)
		}}
		svc := app.NewCheckoutService(e.store, e.carts, hook, app.CheckoutConfig{ShippingFee: 30_000, Now: e.clock.Now})
		buyer := uid()
		s := e.fake.AddSku(catalogfake.NewShop("Late"), 10_000, 5)
		e.cartSet(t, buyer, s.GetSkuId(), 1)

		_, err := svc.Checkout(ctx, checkoutInput(buyer, "late"), (&recorder{}).encode)
		require.ErrorIs(t, err, app.ErrCheckoutExpired)
		orders := buyerOrders(t, e, buyer)
		require.Len(t, orders, 1)
		assert.Equal(t, domain.StatusCancelled, orders[0].Status)
		assert.Equal(t, domain.CancelCheckoutTimeout, *orders[0].CancelReason)
		assert.Equal(t, int32(5), e.fake.Stock(s.GetSkuId()))
		assert.False(t, e.fake.Reserved(orders[0].ID))
		lines, err := e.carts.Lines(ctx, buyer)
		require.NoError(t, err)
		assert.Len(t, lines, 1, "cart kept")
	})
}

// TestSweeperRunBoundsPendingLifetime checks acceptance criterion 2: no order
// stays PENDING longer than PENDING_TIMEOUT + one sweeper interval.
func TestSweeperRunBoundsPendingLifetime(t *testing.T) {
	e := newEnv(t, envOpts{postgres: true})
	const timeout, interval = 400 * time.Millisecond, 200 * time.Millisecond
	realNow := func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }
	sw := app.NewSweeper(e.store, e.catalog, app.SweeperConfig{Interval: interval, PendingTimeout: timeout, Now: realNow})
	o, _ := crashedCheckout(t, e, realNow(), 5)

	rctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- sw.Run(rctx) }()
	t.Cleanup(func() {
		cancel()
		assert.NoError(t, <-done)
	})

	var got domain.Order
	require.Eventually(t, func() bool {
		var err error
		got, err = e.store.GetOrder(ctx, o.ID)
		return err == nil && got.Status == domain.StatusCancelled
	}, timeout+interval+2*time.Second, 20*time.Millisecond)
	lifetime := got.History[len(got.History)-1].At.Sub(o.CreatedAt)
	assert.GreaterOrEqual(t, lifetime, timeout)
	assert.LessOrEqual(t, lifetime, timeout+interval+250*time.Millisecond, "250ms slack for scheduling and the transaction")
}

// TestSweeperSlowCatalogDoesNotDelayCancellation: while catalog is down each
// release burns its full retry budget; cancelling every stale order must not
// wait for those releases, and the releases must not run one by one.
func TestSweeperSlowCatalogDoesNotDelayCancellation(t *testing.T) {
	e := newEnv(t, envOpts{postgres: true})
	sw := app.NewSweeper(e.store, e.catalog, app.SweeperConfig{
		Interval: time.Minute, PendingTimeout: 2 * time.Minute, BatchSize: 2, Now: e.clock.Now,
	})
	var orders []domain.Order
	for range 4 {
		o, _ := crashedCheckout(t, e, e.clock.Now(), 5)
		orders = append(orders, o)
	}
	e.clock.Advance(3 * time.Minute)
	e.fake.SetDelay(catalogfake.ReleaseStock, time.Second) // > the 300ms per-attempt timeout

	start := time.Now()
	n, err := sw.RunOnce(ctx)
	elapsed := time.Since(start)
	require.NoError(t, err)
	assert.Equal(t, 4, n)
	for _, o := range orders {
		got, err := e.store.GetOrder(ctx, o.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusCancelled, got.Status)
	}
	// One release costs ~0.9s (3 attempts × 300ms); four in sequence ≈ 3.6s.
	assert.Less(t, elapsed, 2*time.Second, "releases must run concurrently")
}
