package app_test

import (
	"context"
	"encoding/json"
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

// recorder is an app.Encoder that remembers what it was asked to encode.
// Errors render as 409 here; real status mapping is adapter/http's job.
type recorder struct {
	mu  sync.Mutex
	err error
}

func (r *recorder) encode(res app.CheckoutResult, err error) app.Response {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = err
	if err != nil {
		b, _ := json.Marshal(map[string]string{"error": err.Error()})
		return app.Response{Status: http.StatusConflict, Body: b}
	}
	ids := make([]string, len(res.Orders))
	for i, o := range res.Orders {
		ids[i] = o.ID
	}
	b, _ := json.Marshal(map[string]any{"checkout_id": res.CheckoutID, "order_ids": ids})
	return app.Response{Status: http.StatusCreated, Body: b}
}

func (r *recorder) lastErr() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

func checkoutInput(buyer, key string, skuIDs ...string) app.CheckoutInput {
	return app.CheckoutInput{BuyerID: buyer, IdempotencyKey: key, SkuIDs: skuIDs, Address: testAddress, PaymentMethod: domain.PaymentCOD}
}

func createdOrderIDs(t *testing.T, resp app.Response) []string {
	t.Helper()
	require.Equal(t, http.StatusCreated, resp.Status, string(resp.Body))
	var body struct {
		OrderIDs []string `json:"order_ids"`
	}
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	return body.OrderIDs
}

func buyerOrders(t *testing.T, e *env, buyer string) []domain.Order {
	t.Helper()
	orders, err := e.store.ListOrders(ctx, app.ListQuery{Party: app.PartyBuyer, UserID: buyer, Limit: 100})
	require.NoError(t, err)
	return orders
}

func TestRequestHashIgnoresSkuOrderAndDuplicates(t *testing.T) {
	a := app.RequestHash(checkoutInput("b", "k", "s2", "s1"))
	assert.Equal(t, a, app.RequestHash(checkoutInput("b", "k", "s1", "s2", "s1")))
	assert.NotEqual(t, a, app.RequestHash(checkoutInput("b", "k", "s1")))
	assert.NotEqual(t, app.RequestHash(checkoutInput("b", "k")), app.RequestHash(checkoutInput("b", "k", "s1")), "whole cart differs from a selection")
}

func TestCheckout(t *testing.T) {
	e := newEnv(t, envOpts{postgres: true, redis: true})
	svc := app.NewCheckoutService(e.store, e.carts, e.catalog, app.CheckoutConfig{ShippingFee: 30_000, Now: e.clock.Now})

	t.Run("two shops become two orders pending confirmation", func(t *testing.T) {
		buyer, shopA, shopB := uid(), catalogfake.NewShop("A"), catalogfake.NewShop("B")
		a := e.fake.AddSku(shopA, 100_000, 10)
		b := e.fake.AddSku(shopB, 40_000, 5)
		e.cartSet(t, buyer, a.GetSkuId(), 2)
		e.cartSet(t, buyer, b.GetSkuId(), 1)
		reserves := e.fake.Calls(catalogfake.ReserveStock)

		rec := &recorder{}
		resp, err := svc.Checkout(ctx, checkoutInput(buyer, "k1"), rec.encode)
		require.NoError(t, err)
		ids := createdOrderIDs(t, resp)
		require.Len(t, ids, 2)

		assert.Equal(t, reserves+2, e.fake.Calls(catalogfake.ReserveStock))
		assert.Equal(t, int32(8), e.fake.Stock(a.GetSkuId()))
		assert.Equal(t, int32(4), e.fake.Stock(b.GetSkuId()))
		totals := map[string]int64{}
		checkoutIDs := map[string]bool{}
		for _, id := range ids {
			o, err := e.store.GetOrder(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, domain.StatusPendingConfirmation, o.Status)
			assert.Equal(t, int64(30_000), o.ShippingFee)
			assert.Len(t, o.History, 2)
			assert.True(t, e.fake.Reserved(o.ID))
			assert.Equal(t, []event.Type{event.TypeOrderCreated, event.TypeOrderStatusChanged}, types(e.events(t, id)))
			totals[o.ShopID] = o.Total
			checkoutIDs[o.CheckoutID] = true
		}
		assert.Equal(t, map[string]int64{shopA.ID: 230_000, shopB.ID: 70_000}, totals)
		assert.Len(t, checkoutIDs, 1, "one checkout_id for the whole checkout")

		lines, err := e.carts.Lines(ctx, buyer)
		require.NoError(t, err)
		assert.Empty(t, lines, "purchased lines are removed from the cart")
	})

	t.Run("selected sku_ids only; equivalent retries replay without new reservations", func(t *testing.T) {
		buyer, shop := uid(), catalogfake.NewShop("Sel")
		a := e.fake.AddSku(shop, 10_000, 10)
		b := e.fake.AddSku(shop, 20_000, 10)
		c := e.fake.AddSku(catalogfake.NewShop("Other"), 5_000, 10)
		for _, s := range []string{a.GetSkuId(), b.GetSkuId(), c.GetSkuId()} {
			e.cartSet(t, buyer, s, 1)
		}
		rec := &recorder{}
		first, err := svc.Checkout(ctx, checkoutInput(buyer, "sel", b.GetSkuId(), a.GetSkuId()), rec.encode)
		require.NoError(t, err)
		require.Len(t, createdOrderIDs(t, first), 1)
		reserves := e.fake.Calls(catalogfake.ReserveStock)

		again, err := svc.Checkout(ctx, checkoutInput(buyer, "sel", a.GetSkuId(), b.GetSkuId(), a.GetSkuId()), rec.encode)
		require.NoError(t, err)
		assert.Equal(t, first.Status, again.Status)
		assert.JSONEq(t, string(first.Body), string(again.Body))
		assert.Equal(t, reserves, e.fake.Calls(catalogfake.ReserveStock))

		lines, err := e.carts.Lines(ctx, buyer)
		require.NoError(t, err)
		assert.Equal(t, map[string]int32{c.GetSkuId(): 1}, lines)
	})

	t.Run("same key with a different body is rejected", func(t *testing.T) {
		buyer := uid()
		s := e.fake.AddSku(catalogfake.NewShop("Reuse"), 10_000, 10)
		e.cartSet(t, buyer, s.GetSkuId(), 1)
		rec := &recorder{}
		_, err := svc.Checkout(ctx, checkoutInput(buyer, "reuse"), rec.encode)
		require.NoError(t, err)

		other := checkoutInput(buyer, "reuse")
		other.Address.Line1 = "2 Hai Bà Trưng"
		_, err = svc.Checkout(ctx, other, rec.encode)
		assert.ErrorIs(t, err, app.ErrIdempotencyKeyReused)
	})

	t.Run("same key still in progress is a conflict", func(t *testing.T) {
		buyer := uid()
		in := checkoutInput(buyer, "busy")
		_, created, err := e.store.BeginIdempotency(ctx, buyer, "busy", app.RequestHash(in), e.clock.Now(), time.Time{})
		require.NoError(t, err)
		require.True(t, created)
		_, err = svc.Checkout(ctx, in, (&recorder{}).encode)
		assert.ErrorIs(t, err, app.ErrIdempotencyInProgress)
	})

	t.Run("a key orphaned for longer than ClaimTimeout is reclaimed", func(t *testing.T) {
		reclaiming := app.NewCheckoutService(e.store, e.carts, e.catalog, app.CheckoutConfig{
			ShippingFee: 30_000, Now: e.clock.Now, ClaimTimeout: 3 * time.Minute,
		})
		buyer := uid()
		s := e.fake.AddSku(catalogfake.NewShop("Orphan"), 10_000, 5)
		e.cartSet(t, buyer, s.GetSkuId(), 1)
		in := checkoutInput(buyer, "orphan")
		// A pod crashed right after claiming the key.
		_, created, err := e.store.BeginIdempotency(ctx, buyer, "orphan", app.RequestHash(in), e.clock.Now().Add(-2*time.Minute), time.Time{})
		require.NoError(t, err)
		require.True(t, created)

		_, err = reclaiming.Checkout(ctx, in, (&recorder{}).encode)
		require.ErrorIs(t, err, app.ErrIdempotencyInProgress, "younger than ClaimTimeout: maybe still running")

		e.clock.Advance(2 * time.Minute)
		resp, err := reclaiming.Checkout(ctx, in, (&recorder{}).encode)
		require.NoError(t, err)
		assert.Len(t, createdOrderIDs(t, resp), 1)
		replay, err := reclaiming.Checkout(ctx, in, (&recorder{}).encode)
		require.NoError(t, err)
		assert.JSONEq(t, string(resp.Body), string(replay.Body), "the reclaiming run's response is stored")
	})

	t.Run("a run whose key was reclaimed cannot commit and cleans up after itself", func(t *testing.T) {
		buyer := uid()
		s := e.fake.AddSku(catalogfake.NewShop("Zombie"), 10_000, 5)
		e.cartSet(t, buyer, s.GetSkuId(), 2)
		in := checkoutInput(buyer, "zombie")
		hook := &sweepDuringReserve{Catalog: e.catalog, run: func() {
			// While this run is stuck, a retry takes the key over.
			later := e.clock.Now().Add(10 * time.Minute)
			_, created, err := e.store.BeginIdempotency(ctx, buyer, "zombie", app.RequestHash(in), later, later.Add(-time.Minute))
			assert.NoError(t, err)
			assert.True(t, created)
		}}
		zombie := app.NewCheckoutService(e.store, e.carts, hook, app.CheckoutConfig{ShippingFee: 30_000, Now: e.clock.Now})

		_, err := zombie.Checkout(ctx, in, (&recorder{}).encode)
		require.ErrorIs(t, err, app.ErrIdempotencyClaimLost)
		orders := buyerOrders(t, e, buyer)
		require.Len(t, orders, 1)
		assert.Equal(t, domain.StatusCancelled, orders[0].Status)
		assert.Equal(t, int32(5), e.fake.Stock(s.GetSkuId()), "its reservation is released")
		rec, created, err := e.store.BeginIdempotency(ctx, buyer, "zombie", app.RequestHash(in), e.clock.Now(), time.Time{})
		require.NoError(t, err)
		assert.False(t, created, "the key still belongs to the retry")
		assert.Nil(t, rec.Response)
	})

	t.Run("empty cart is a stored rejection without catalog calls", func(t *testing.T) {
		buyer, rec := uid(), &recorder{}
		calls := e.fake.Calls(catalogfake.GetSkus)
		resp, err := svc.Checkout(ctx, checkoutInput(buyer, "empty"), rec.encode)
		require.NoError(t, err)
		assert.ErrorIs(t, rec.lastErr(), app.ErrEmptyCart)
		replay, err := svc.Checkout(ctx, checkoutInput(buyer, "empty"), rec.encode)
		require.NoError(t, err)
		assert.JSONEq(t, string(resp.Body), string(replay.Body))
		assert.Equal(t, calls, e.fake.Calls(catalogfake.GetSkus))
	})

	t.Run("sku_ids not in the cart", func(t *testing.T) {
		buyer, missing, rec := uid(), uid(), &recorder{}
		s := e.fake.AddSku(catalogfake.NewShop("Miss"), 10_000, 10)
		e.cartSet(t, buyer, s.GetSkuId(), 1)
		_, err := svc.Checkout(ctx, checkoutInput(buyer, "miss", s.GetSkuId(), missing), rec.encode)
		require.NoError(t, err)
		var sel *app.SelectionError
		require.ErrorAs(t, rec.lastErr(), &sel)
		assert.Equal(t, []string{missing}, sel.NotInCart)
	})

	t.Run("unavailable and own-shop lines create nothing", func(t *testing.T) {
		buyer, rec := uid(), &recorder{}
		mine := catalogfake.NewShop("Mine")
		mine.SellerUserID = buyer
		own := e.fake.AddSku(mine, 10_000, 10)
		off := e.fake.AddSku(catalogfake.NewShop("Off"), 10_000, 10)
		e.fake.SetAvailable(off.GetSkuId(), false)
		ok := e.fake.AddSku(catalogfake.NewShop("Ok"), 10_000, 10)
		for _, s := range []string{own.GetSkuId(), off.GetSkuId(), ok.GetSkuId()} {
			e.cartSet(t, buyer, s, 1)
		}
		reserves := e.fake.Calls(catalogfake.ReserveStock)

		_, err := svc.Checkout(ctx, checkoutInput(buyer, "bad-lines"), rec.encode)
		require.NoError(t, err)
		var lp *domain.LineProblemsError
		require.ErrorAs(t, rec.lastErr(), &lp)
		assert.ElementsMatch(t, []domain.LineProblem{
			{SkuID: own.GetSkuId(), Reason: domain.ProblemOwnShop},
			{SkuID: off.GetSkuId(), Reason: domain.ProblemUnavailable},
		}, lp.Problems)
		assert.Empty(t, buyerOrders(t, e, buyer))
		assert.Equal(t, reserves, e.fake.Calls(catalogfake.ReserveStock))
		lines, err := e.carts.Lines(ctx, buyer)
		require.NoError(t, err)
		assert.Len(t, lines, 3)
	})

	t.Run("catalog unavailable is not stored so the same key can be retried", func(t *testing.T) {
		buyer := uid()
		s := e.fake.AddSku(catalogfake.NewShop("Down"), 10_000, 10)
		e.cartSet(t, buyer, s.GetSkuId(), 1)
		e.fake.FailNext(catalogfake.GetSkus, codes.Unavailable, 3)

		_, err := svc.Checkout(ctx, checkoutInput(buyer, "down"), (&recorder{}).encode)
		require.ErrorIs(t, err, app.ErrCatalogUnavailable)
		resp, err := svc.Checkout(ctx, checkoutInput(buyer, "down"), (&recorder{}).encode)
		require.NoError(t, err)
		assert.Len(t, createdOrderIDs(t, resp), 1)
	})

	t.Run("a cancelled request context still completes the checkout", func(t *testing.T) {
		buyer := uid()
		s := e.fake.AddSku(catalogfake.NewShop("Gone"), 10_000, 10)
		e.cartSet(t, buyer, s.GetSkuId(), 1)
		cctx, cancel := context.WithCancel(ctx)
		cancel()

		resp, err := svc.Checkout(cctx, checkoutInput(buyer, "gone"), (&recorder{}).encode)
		require.NoError(t, err)
		ids := createdOrderIDs(t, resp)
		o, err := e.store.GetOrder(ctx, ids[0])
		require.NoError(t, err)
		assert.Equal(t, domain.StatusPendingConfirmation, o.Status)
	})
}
