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

// checkoutInputWith is checkoutInput with another payment method.
func checkoutInputWith(buyer, key, method string, skuIDs ...string) app.CheckoutInput {
	in := checkoutInput(buyer, key, skuIDs...)
	in.PaymentMethod = method
	return in
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

	// A zombie run is stuck in ReserveStock while a retry of the same request
	// takes its key over; whatever the retry then does, the zombie must not
	// store or return anything and must cancel and release its own orders.
	zombieCases := []struct {
		name  string
		stock int32 // < 2 → the zombie's reservation fails (Tx B path)
		retry func(t *testing.T, buyer string, claim time.Time)
		check func(t *testing.T, rec app.IdempotencyRecord, created bool)
	}{
		{
			name: "retry still running", stock: 5,
			retry: func(*testing.T, string, time.Time) {},
			check: func(t *testing.T, rec app.IdempotencyRecord, created bool) {
				assert.False(t, created, "the key still belongs to the retry")
				assert.Nil(t, rec.Response)
			},
		},
		{
			name: "retry completed", stock: 5,
			retry: func(t *testing.T, buyer string, claim time.Time) {
				assert.NoError(t, e.store.InTx(ctx, func(tx app.Tx) error {
					return tx.CompleteIdempotencyKey(ctx, buyer, "zombie", claim, app.Response{Status: 201, Body: []byte(`{"retry":true}`)})
				}))
			},
			check: func(t *testing.T, rec app.IdempotencyRecord, created bool) {
				assert.False(t, created)
				require.NotNil(t, rec.Response)
				assert.JSONEq(t, `{"retry":true}`, string(rec.Response.Body), "the retry's answer is not replaced")
			},
		},
		{
			name: "retry released the key", stock: 5,
			retry: func(t *testing.T, buyer string, claim time.Time) {
				assert.NoError(t, e.store.DeleteIdempotencyKey(ctx, buyer, "zombie", claim))
			},
			check: func(t *testing.T, _ app.IdempotencyRecord, created bool) {
				assert.True(t, created, "the zombie left no orphaned claim behind")
			},
		},
		{
			name: "zombie's reservation fails", stock: 1,
			retry: func(*testing.T, string, time.Time) {},
			check: func(t *testing.T, rec app.IdempotencyRecord, created bool) {
				assert.False(t, created)
				assert.Nil(t, rec.Response, "the zombie's rejection is not stored under the retry's claim")
			},
		},
	}
	for _, tc := range zombieCases {
		t.Run("a run whose key was reclaimed cleans up after itself: "+tc.name, func(t *testing.T) {
			buyer := uid()
			s := e.fake.AddSku(catalogfake.NewShop("Zombie"), 10_000, tc.stock)
			e.cartSet(t, buyer, s.GetSkuId(), 2)
			in := checkoutInput(buyer, "zombie")
			hook := &sweepDuringReserve{Catalog: e.catalog, run: func() {
				later := e.clock.Now().Add(10 * time.Minute)
				rec, created, err := e.store.BeginIdempotency(ctx, buyer, "zombie", app.RequestHash(in), later, later.Add(-time.Minute))
				assert.NoError(t, err)
				assert.True(t, created)
				tc.retry(t, buyer, rec.ClaimedAt)
				e.clock.Advance(time.Second) // the zombie resumes later than it claimed
			}}
			zombie := app.NewCheckoutService(e.store, e.carts, hook, app.CheckoutConfig{ShippingFee: 30_000, Now: e.clock.Now})

			_, err := zombie.Checkout(ctx, in, (&recorder{}).encode)
			require.ErrorIs(t, err, app.ErrIdempotencyClaimLost)
			orders := buyerOrders(t, e, buyer)
			require.Len(t, orders, 1)
			assert.Equal(t, domain.StatusCancelled, orders[0].Status, "cancelled now, not left to the sweeper")
			assert.Equal(t, tc.stock, e.fake.Stock(s.GetSkuId()), "its reservation is released")
			rec, created, err := e.store.BeginIdempotency(ctx, buyer, "zombie", app.RequestHash(in), e.clock.Now(), time.Time{})
			require.NoError(t, err)
			tc.check(t, rec, created)
		})
	}

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

// TestCheckoutOnline covers the P2' online checkout: VNPAY orders are created
// in AWAITING_PAYMENT/UNPAID with one shared payment deadline and a commission
// snapshot, and a method missing from OnlineMethods is rejected before the
// idempotency key is claimed.
func TestCheckoutOnline(t *testing.T) {
	e := newEnv(t, envOpts{postgres: true, redis: true})
	svc := app.NewCheckoutService(e.store, e.carts, e.catalog, app.CheckoutConfig{
		ShippingFee: 30_000, OnlineMethods: []string{"VNPAY", "STRIPE"},
		CommissionRateBps: 500, PaymentTimeout: 30 * time.Minute, Now: e.clock.Now,
	})
	vnpayOnly := app.NewCheckoutService(e.store, e.carts, e.catalog, app.CheckoutConfig{
		ShippingFee: 30_000, OnlineMethods: []string{"VNPAY"},
		CommissionRateBps: 500, PaymentTimeout: 30 * time.Minute, Now: e.clock.Now,
	})

	t.Run("online checkout from two shops", func(t *testing.T) {
		buyer, shopA, shopB := uid(), catalogfake.NewShop("Online A"), catalogfake.NewShop("Online B")
		a := e.fake.AddSku(shopA, 120_000, 10)
		b := e.fake.AddSku(shopB, 45_000, 10)
		e.cartSet(t, buyer, a.GetSkuId(), 2)
		e.cartSet(t, buyer, b.GetSkuId(), 1)
		reserves := e.fake.Calls(catalogfake.ReserveStock)

		rec := &recorder{}
		resp, err := svc.Checkout(ctx, checkoutInputWith(buyer, "on-1", domain.PaymentVNPay), rec.encode)
		require.NoError(t, err)
		ids := createdOrderIDs(t, resp)
		require.Len(t, ids, 2)
		assert.Equal(t, reserves+2, e.fake.Calls(catalogfake.ReserveStock))

		wantDeadline := e.clock.Now().Add(30 * time.Minute)
		commissions := map[string]int64{shopA.ID: 12_000, shopB.ID: 2_250}
		for _, id := range ids {
			o, err := e.store.GetOrder(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, domain.StatusAwaitingPayment, o.Status)
			assert.Equal(t, domain.PaymentUnpaid, o.PaymentStatus)
			assert.Equal(t, domain.PaymentVNPay, o.PaymentMethod)
			require.NotNil(t, o.PaymentDeadline, "an online order carries a payment deadline")
			assert.Equal(t, wantDeadline, *o.PaymentDeadline, "one deadline for the whole checkout")
			assert.Equal(t, int32(500), o.CommissionRateBps)
			assert.Equal(t, commissions[o.ShopID], o.CommissionAmount)
			assert.True(t, e.fake.Reserved(o.ID))

			evs := e.events(t, id)
			assert.Equal(t, []event.Type{event.TypeOrderCreated, event.TypeOrderStatusChanged}, types(evs))
			created, err := event.DecodeData[event.OrderCreated](evs[0])
			require.NoError(t, err)
			assert.Equal(t, domain.PaymentVNPay, created.PaymentMethod)
			assert.Equal(t, int32(500), created.CommissionRateBps)
			assert.Equal(t, o.CommissionAmount, created.CommissionAmount)
			changed, err := event.DecodeData[event.OrderStatusChanged](evs[1])
			require.NoError(t, err)
			assert.Equal(t, "PENDING", changed.From)
			assert.Equal(t, "AWAITING_PAYMENT", changed.To)
		}
	})

	t.Run("online replay returns the stored response without new reservations", func(t *testing.T) {
		buyer := uid()
		s := e.fake.AddSku(catalogfake.NewShop("Online Replay"), 10_000, 10)
		e.cartSet(t, buyer, s.GetSkuId(), 1)
		in := checkoutInputWith(buyer, "on-replay", domain.PaymentVNPay)

		first, err := svc.Checkout(ctx, in, (&recorder{}).encode)
		require.NoError(t, err)
		reserves := e.fake.Calls(catalogfake.ReserveStock)

		again, err := svc.Checkout(ctx, in, (&recorder{}).encode)
		require.NoError(t, err)
		assert.Equal(t, first.Status, again.Status)
		// The stored response comes back from a jsonb column, which
		// normalises key order, so compare the decoded JSON, not the bytes.
		assert.JSONEq(t, string(first.Body), string(again.Body), "the replayed response is the stored one")
		assert.Equal(t, reserves, e.fake.Calls(catalogfake.ReserveStock), "the replay reserves nothing")

		rec, found, err := e.store.IdempotencyKey(ctx, buyer, "on-replay")
		require.NoError(t, err)
		require.True(t, found)
		require.NotNil(t, rec.Response)
		assert.JSONEq(t, string(first.Body), string(rec.Response.Body))
	})

	t.Run("a method missing from OnlineMethods is rejected before the key is claimed", func(t *testing.T) {
		buyer := uid()
		s := e.fake.AddSku(catalogfake.NewShop("Disabled Method"), 10_000, 10)
		e.cartSet(t, buyer, s.GetSkuId(), 1)
		getSkus := e.fake.Calls(catalogfake.GetSkus)
		reserves := e.fake.Calls(catalogfake.ReserveStock)

		_, err := vnpayOnly.Checkout(ctx, checkoutInputWith(buyer, "on-off", domain.PaymentStripe), (&recorder{}).encode)
		assert.ErrorIs(t, err, app.ErrPaymentMethodNotEnabled)

		_, found, err := e.store.IdempotencyKey(ctx, buyer, "on-off")
		require.NoError(t, err)
		assert.False(t, found, "a rejected method leaves the key untouched")
		assert.Equal(t, getSkus, e.fake.Calls(catalogfake.GetSkus), "no catalog calls")
		assert.Equal(t, reserves, e.fake.Calls(catalogfake.ReserveStock))

		// The same key still works for an enabled method.
		_, err = vnpayOnly.Checkout(ctx, checkoutInputWith(buyer, "on-off", domain.PaymentVNPay), (&recorder{}).encode)
		require.NoError(t, err)
	})

	t.Run("COD keeps PENDING_CONFIRMATION, no deadline and no commission", func(t *testing.T) {
		buyer, shop := uid(), catalogfake.NewShop("COD Unchanged")
		s := e.fake.AddSku(shop, 120_000, 10)
		e.cartSet(t, buyer, s.GetSkuId(), 2)

		// The online-enabled service must not change COD behaviour.
		resp, err := svc.Checkout(ctx, checkoutInput(buyer, "cod-unchanged"), (&recorder{}).encode)
		require.NoError(t, err)
		o, err := e.store.GetOrder(ctx, createdOrderIDs(t, resp)[0])
		require.NoError(t, err)
		assert.Equal(t, domain.StatusPendingConfirmation, o.Status)
		assert.Equal(t, domain.PaymentNone, o.PaymentStatus)
		assert.Nil(t, o.PaymentDeadline)
		assert.Zero(t, o.CommissionRateBps)
		assert.Zero(t, o.CommissionAmount)
	})
}
