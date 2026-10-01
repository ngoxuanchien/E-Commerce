package app_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/catalog/catalogfake"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

// sweepBeforeTxB runs `run` after the reservation of the order holding shortSKU
// failed and once every other order's reservation is done, so the sweeper has
// cancelled all PENDING orders of the checkout before Tx B starts.
type sweepBeforeTxB struct {
	app.Catalog
	shortSKU     string
	reserved     chan struct{} // closed once the other orders reserved
	reservedOnce sync.Once
	run          func()
}

func (c *sweepBeforeTxB) ReserveStock(ctx context.Context, orderID string, lines []domain.Line) error {
	err := c.Catalog.ReserveStock(ctx, orderID, lines)
	for _, l := range lines {
		if l.SkuID != c.shortSKU {
			continue
		}
		select {
		case <-c.reserved: // the sweeper releases the stock of every cancelled order
		case <-ctx.Done():
			return err
		}
		c.run()
		return err
	}
	c.reservedOnce.Do(func() { close(c.reserved) })
	return err
}

func TestCheckoutCompensation(t *testing.T) {
	e := newEnv(t, envOpts{postgres: true, redis: true})
	svc := app.NewCheckoutService(e.store, e.carts, e.catalog, app.CheckoutConfig{ShippingFee: 30_000, Now: e.clock.Now})

	assertAllCancelled := func(t *testing.T, buyer, reason string) []domain.Order {
		t.Helper()
		orders := buyerOrders(t, e, buyer)
		require.Len(t, orders, 2)
		for _, o := range orders {
			assert.Equal(t, domain.StatusCancelled, o.Status)
			require.NotNil(t, o.CancelReason)
			assert.Equal(t, reason, *o.CancelReason)
			assert.Equal(t, domain.RoleSystem, o.History[len(o.History)-1].ActorRole)
			assert.Contains(t, e.fake.ReleasedOrders(), o.ID)
			assert.False(t, e.fake.Reserved(o.ID))
			assert.Equal(t, []event.Type{event.TypeOrderStatusChanged, event.TypeOrderCancelled}, types(e.events(t, o.ID)))
		}
		return orders
	}

	t.Run("out of stock in one shop cancels and releases every order", func(t *testing.T) {
		buyer, rec := uid(), &recorder{}
		a := e.fake.AddSku(catalogfake.NewShop("A"), 100_000, 10)
		b := e.fake.AddSku(catalogfake.NewShop("B"), 40_000, 1)
		e.cartSet(t, buyer, a.GetSkuId(), 2)
		e.cartSet(t, buyer, b.GetSkuId(), 3)

		resp, err := svc.Checkout(ctx, checkoutInput(buyer, "oos"), rec.encode)
		require.NoError(t, err)
		var oos *app.OutOfStockError
		require.ErrorAs(t, rec.lastErr(), &oos)
		assert.Equal(t, []domain.Shortage{{SkuID: b.GetSkuId(), Requested: 3, Available: 1}}, oos.Shortages)

		assertAllCancelled(t, buyer, domain.CancelOutOfStock)
		assert.Equal(t, int32(10), e.fake.Stock(a.GetSkuId()), "shop A's reservation was returned")
		lines, err := e.carts.Lines(ctx, buyer)
		require.NoError(t, err)
		assert.Len(t, lines, 2, "the cart is kept")

		replay, err := svc.Checkout(ctx, checkoutInput(buyer, "oos"), rec.encode)
		require.NoError(t, err)
		assert.JSONEq(t, string(resp.Body), string(replay.Body))
	})

	t.Run("shortages from several shops are merged", func(t *testing.T) {
		buyer, rec := uid(), &recorder{}
		a := e.fake.AddSku(catalogfake.NewShop("A2"), 1_000, 0)
		b := e.fake.AddSku(catalogfake.NewShop("B2"), 1_000, 0)
		e.cartSet(t, buyer, a.GetSkuId(), 1)
		e.cartSet(t, buyer, b.GetSkuId(), 1)
		_, err := svc.Checkout(ctx, checkoutInput(buyer, "oos2"), rec.encode)
		require.NoError(t, err)
		var oos *app.OutOfStockError
		require.ErrorAs(t, rec.lastErr(), &oos)
		assert.Len(t, oos.Shortages, 2)
	})

	t.Run("catalog down while reserving cancels with RESERVE_FAILED and frees the key", func(t *testing.T) {
		buyer := uid()
		a := e.fake.AddSku(catalogfake.NewShop("A3"), 10_000, 10)
		b := e.fake.AddSku(catalogfake.NewShop("B3"), 10_000, 10)
		e.cartSet(t, buyer, a.GetSkuId(), 1)
		e.cartSet(t, buyer, b.GetSkuId(), 1)
		e.fake.FailReserveForSkus(codes.Unavailable, b.GetSkuId())

		_, err := svc.Checkout(ctx, checkoutInput(buyer, "flaky"), (&recorder{}).encode)
		require.ErrorIs(t, err, app.ErrCatalogUnavailable)
		assertAllCancelled(t, buyer, domain.CancelReserveFailed)
		assert.Equal(t, int32(10), e.fake.Stock(a.GetSkuId()))

		e.fake.FailReserveForSkus(codes.OK, b.GetSkuId())
		resp, err := svc.Checkout(ctx, checkoutInput(buyer, "flaky"), (&recorder{}).encode)
		require.NoError(t, err)
		assert.Len(t, createdOrderIDs(t, resp), 2)
	})

	t.Run("reservation timeouts are retried then reported as unavailable", func(t *testing.T) {
		buyer := uid()
		s := e.fake.AddSku(catalogfake.NewShop("Slow"), 10_000, 10)
		e.cartSet(t, buyer, s.GetSkuId(), 1)
		e.fake.SetDelay(catalogfake.ReserveStock, time.Second)
		t.Cleanup(func() { e.fake.SetDelay(catalogfake.ReserveStock, 0) })
		calls := e.fake.Calls(catalogfake.ReserveStock)

		_, err := svc.Checkout(ctx, checkoutInput(buyer, "slow"), (&recorder{}).encode)
		require.ErrorIs(t, err, app.ErrCatalogUnavailable)
		assert.Equal(t, calls+3, e.fake.Calls(catalogfake.ReserveStock), "1 attempt + 2 retries")
		orders := buyerOrders(t, e, buyer)
		require.Len(t, orders, 1)
		assert.Equal(t, domain.StatusCancelled, orders[0].Status)
	})

	t.Run("a transient reserve failure is retried transparently", func(t *testing.T) {
		buyer := uid()
		s := e.fake.AddSku(catalogfake.NewShop("Blip"), 10_000, 10)
		e.cartSet(t, buyer, s.GetSkuId(), 1)
		e.fake.FailNext(catalogfake.ReserveStock, codes.Unavailable, 1)
		resp, err := svc.Checkout(ctx, checkoutInput(buyer, "blip"), (&recorder{}).encode)
		require.NoError(t, err)
		assert.Len(t, createdOrderIDs(t, resp), 1)
	})

	// The sweeper cancels the checkout's PENDING orders between the failed
	// reservation and Tx B, so Tx B loses the optimistic lock. The buyer must
	// still get the real cause — OUT_OF_STOCK with its shortages — not a
	// conflict on an internal race.
	t.Run("sweeper racing Tx B still answers OUT_OF_STOCK", func(t *testing.T) {
		buyer, rec := uid(), &recorder{}
		a := e.fake.AddSku(catalogfake.NewShop("A4"), 100_000, 10)
		b := e.fake.AddSku(catalogfake.NewShop("B4"), 40_000, 1)
		e.cartSet(t, buyer, a.GetSkuId(), 2)
		e.cartSet(t, buyer, b.GetSkuId(), 3)
		sw := app.NewSweeper(e.store, e.catalog, nil, app.SweeperConfig{
			Interval: time.Minute, PendingTimeout: 2 * time.Minute, Now: e.clock.Now,
		})
		hook := &sweepBeforeTxB{Catalog: e.catalog, shortSKU: b.GetSkuId(), reserved: make(chan struct{}), run: func() {
			e.clock.Advance(3 * time.Minute) // past PENDING_TIMEOUT
			_, err := sw.RunOnce(ctx)
			assert.NoError(t, err)
		}}
		racing := app.NewCheckoutService(e.store, e.carts, hook, app.CheckoutConfig{ShippingFee: 30_000, Now: e.clock.Now})

		resp, err := racing.Checkout(ctx, checkoutInput(buyer, "race"), rec.encode)
		require.NoError(t, err)
		assert.Equal(t, http.StatusConflict, resp.Status)
		assert.Contains(t, string(resp.Body), "*app.OutOfStockError", "the real cause, not the lost race")
		var oos *app.OutOfStockError
		require.ErrorAs(t, rec.lastErr(), &oos)
		assert.Equal(t, []domain.Shortage{{SkuID: b.GetSkuId(), Requested: 3, Available: 1}}, oos.Shortages)

		for _, o := range buyerOrders(t, e, buyer) {
			assert.Equal(t, domain.StatusCancelled, o.Status, "the sweeper cancelled them, Tx B must not leave any PENDING")
			assert.False(t, e.fake.Reserved(o.ID), "its reservation is released")
		}
		replay, err := racing.Checkout(ctx, checkoutInput(buyer, "race"), rec.encode)
		require.NoError(t, err)
		assert.JSONEq(t, string(resp.Body), string(replay.Body), "the OUT_OF_STOCK answer is stored under the claim")
	})
}
