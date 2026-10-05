package itest

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/catalog/catalogfake"
)

func (h *harness) testCartCRUD(t *testing.T) {
	b, shop := newBuyer(), catalogfake.NewShop("Cart Shop")
	s1 := h.fake.AddSku(shop, 100_000, 3)
	s2 := h.fake.AddSku(shop, 50_000, 10)

	status, body := h.do(t, b, http.MethodGet, "/api/cart", nil)
	require.Equal(t, http.StatusOK, status)
	assert.JSONEq(t, `{"shops":[],"subtotal":0}`, string(body))

	status, body = h.do(t, b, http.MethodPut, "/api/cart/items/"+s1.GetSkuId(), map[string]int{"quantity": 5})
	require.Equal(t, http.StatusOK, status, string(body))
	cart := decode[cartResp](t, body)
	require.Len(t, cart.Shops, 1)
	assert.Equal(t, shop.Slug, cart.Shops[0].Shop.Slug)
	assert.Equal(t, int32(5), cart.Shops[0].Items[0].Quantity)
	assert.Equal(t, int32(3), cart.Shops[0].Items[0].Stock)
	assert.Equal(t, int64(500_000), cart.Subtotal)

	h.addToCart(t, b, s2.GetSkuId(), 1)
	h.addToCart(t, b, s1.GetSkuId(), 2)
	status, body = h.do(t, b, http.MethodDelete, "/api/cart/items/"+s2.GetSkuId(), nil)
	require.Equal(t, http.StatusOK, status)
	cart = decode[cartResp](t, body)
	assert.Len(t, cart.Shops[0].Items, 1)
	assert.Equal(t, int64(200_000), cart.Subtotal)

	status, _ = h.doRaw(t, b, http.MethodDelete, "/api/cart/items/not-a-uuid", "")
	assert.Equal(t, http.StatusOK, status)
}

func (h *harness) testCartRejects(t *testing.T) {
	shop := catalogfake.NewShop("Own Shop")
	own := h.fake.AddSku(shop, 10_000, 5)
	status, body := h.do(t, sellerOf(shop), http.MethodPut, "/api/cart/items/"+own.GetSkuId(), map[string]int{"quantity": 1})
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "CONFLICT", decode[errResp](t, body).Error.Code)

	status, body = h.do(t, newBuyer(), http.MethodPut, "/api/cart/items/"+newID(), map[string]int{"quantity": 1})
	assert.Equal(t, http.StatusNotFound, status)
	assert.Equal(t, "NOT_FOUND", decode[errResp](t, body).Error.Code)

	off := h.fake.AddSku(catalogfake.NewShop("Inactive"), 10_000, 5)
	h.fake.SetAvailable(off.GetSkuId(), false)
	status, _ = h.do(t, newBuyer(), http.MethodPut, "/api/cart/items/"+off.GetSkuId(), map[string]int{"quantity": 1})
	assert.Equal(t, http.StatusConflict, status)
}

func (h *harness) testCartFlagsUnavailable(t *testing.T) {
	b := newBuyer()
	s := h.fake.AddSku(catalogfake.NewShop("Flag"), 10_000, 5)
	h.addToCart(t, b, s.GetSkuId(), 2)
	h.fake.SetAvailable(s.GetSkuId(), false)

	status, body := h.do(t, b, http.MethodGet, "/api/cart", nil)
	require.Equal(t, http.StatusOK, status)
	cart := decode[cartResp](t, body)
	assert.False(t, cart.Shops[0].Items[0].Available)
	assert.Zero(t, cart.Subtotal)
}

func (h *harness) testCartCatalogDown(t *testing.T) {
	b := newBuyer()
	h.addToCart(t, b, h.fake.AddSku(catalogfake.NewShop("Down"), 10_000, 5).GetSkuId(), 1)
	h.fake.FailNext(catalogfake.GetSkus, codes.Unavailable, 3)
	status, body := h.do(t, b, http.MethodGet, "/api/cart", nil)
	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Equal(t, "UPSTREAM_UNAVAILABLE", decode[errResp](t, body).Error.Code)
}

func (h *harness) testCheckoutTwoShops(t *testing.T) {
	b, shopA, shopB := newBuyer(), catalogfake.NewShop("Shop A"), catalogfake.NewShop("Shop B")
	a := h.fake.AddSku(shopA, 120_000, 10)
	bb := h.fake.AddSku(shopB, 45_000, 10)
	h.addToCart(t, b, a.GetSkuId(), 2)
	h.addToCart(t, b, bb.GetSkuId(), 1)
	reserves := h.fake.Calls(catalogfake.ReserveStock)

	status, body := h.checkout(t, b, "ck-1", checkoutBody())
	require.Equal(t, http.StatusCreated, status, string(body))
	res := decode[checkoutResp](t, body)
	require.Len(t, res.Orders, 2)
	assert.Equal(t, reserves+2, h.fake.Calls(catalogfake.ReserveStock))
	totals := map[string]int64{}
	slugs := map[string]string{shopA.ID: shopA.Slug, shopB.ID: shopB.Slug}
	for _, o := range res.Orders {
		assert.Equal(t, "PENDING_CONFIRMATION", o.Status)
		assert.Equal(t, res.CheckoutID, o.CheckoutID)
		assert.Equal(t, "NONE", o.PaymentStatus, "COD collects nothing online")
		assert.Nil(t, o.PaymentDeadline)
		assert.Equal(t, int32(0), o.CommissionRateBps)
		assert.Zero(t, o.CommissionAmount)
		assert.Equal(t, slugs[o.Shop.ID], o.Shop.Slug)
		totals[o.Shop.ID] = o.Total
		evs := h.events.waitTypes(t, o.ID, event.TypeOrderCreated, event.TypeOrderStatusChanged)
		created, err := event.DecodeData[event.OrderCreated](evs[0])
		require.NoError(t, err)
		assert.Equal(t, o.Total, created.Total)
		assert.Equal(t, res.CheckoutID, created.CheckoutID)
		assert.Equal(t, "COD", created.PaymentMethod)
	}
	assert.Equal(t, map[string]int64{shopA.ID: 270_000, shopB.ID: 75_000}, totals)

	status, body = h.do(t, b, http.MethodGet, "/api/cart", nil)
	require.Equal(t, http.StatusOK, status)
	assert.JSONEq(t, `{"shops":[],"subtotal":0}`, string(body), "purchased lines leave the cart")

	status, body = h.do(t, b, http.MethodGet, "/api/orders", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Len(t, decode[pageResp](t, body).Items, 2)

	// Same key, same body → identical response, no new reservations.
	status2, body2 := h.checkout(t, b, "ck-1", checkoutBody())
	assert.Equal(t, http.StatusCreated, status2)
	assert.JSONEq(t, string(res.mustJSON(t)), string(decode[checkoutResp](t, body2).mustJSON(t)))
	assert.Equal(t, reserves+2, h.fake.Calls(catalogfake.ReserveStock))

	// Same key, different body → 422.
	other := checkoutBody()
	other["shipping_address"] = map[string]string{"full_name": "B", "phone": "1", "line1": "x", "ward": "w", "district": "d", "province": "p"}
	status3, body3 := h.checkout(t, b, "ck-1", other)
	assert.Equal(t, http.StatusUnprocessableEntity, status3)
	assert.Equal(t, "IDEMPOTENCY_KEY_REUSED", decode[errResp](t, body3).Error.Code)
}

// testCheckoutOnline pays with VNPAY: both orders are created in
// AWAITING_PAYMENT/UNPAID with one shared payment deadline and a commission
// snapshot on the events.
func (h *harness) testCheckoutOnline(t *testing.T) {
	b, shopA, shopB := newBuyer(), catalogfake.NewShop("Online Shop A"), catalogfake.NewShop("Online Shop B")
	a := h.fake.AddSku(shopA, 120_000, 10)
	bb := h.fake.AddSku(shopB, 45_000, 10)
	h.addToCart(t, b, a.GetSkuId(), 2)
	h.addToCart(t, b, bb.GetSkuId(), 1)

	status, body := h.checkout(t, b, "ck-vnpay", checkoutBodyWith("VNPAY"))
	require.Equal(t, http.StatusCreated, status, string(body))
	res := decode[checkoutResp](t, body)
	require.Len(t, res.Orders, 2)

	commissions := map[string]int64{shopA.ID: 12_000, shopB.ID: 2_250}
	var deadline string
	for i, o := range res.Orders {
		assert.Equal(t, "AWAITING_PAYMENT", o.Status)
		assert.Equal(t, "UNPAID", o.PaymentStatus)
		assert.Equal(t, "VNPAY", o.PaymentMethod)
		require.NotNil(t, o.PaymentDeadline, "an online order carries a payment deadline")
		if i == 0 {
			deadline = *o.PaymentDeadline
		}
		assert.Equal(t, deadline, *o.PaymentDeadline, "one deadline for the whole checkout")
		assert.Equal(t, int32(500), o.CommissionRateBps)
		assert.Equal(t, commissions[o.Shop.ID], o.CommissionAmount)

		evs := h.events.waitTypes(t, o.ID, event.TypeOrderCreated, event.TypeOrderStatusChanged)
		created, err := event.DecodeData[event.OrderCreated](evs[0])
		require.NoError(t, err)
		assert.Equal(t, "VNPAY", created.PaymentMethod)
		assert.Equal(t, int32(500), created.CommissionRateBps)
		assert.Equal(t, o.CommissionAmount, created.CommissionAmount)
		changed, err := event.DecodeData[event.OrderStatusChanged](evs[1])
		require.NoError(t, err)
		assert.Equal(t, "PENDING", changed.From)
		assert.Equal(t, "AWAITING_PAYMENT", changed.To)
	}

	status, body = h.do(t, b, http.MethodGet, "/api/cart", nil)
	require.Equal(t, http.StatusOK, status)
	assert.JSONEq(t, `{"shops":[],"subtotal":0}`, string(body), "purchased lines leave the cart")
}

// testCheckoutMethodNotEnabled checks that a method missing from
// ONLINE_PAYMENT_METHODS is a 400 VALIDATION_ERROR on payment_method and that
// the rejected key is left untouched.
func (h *harness) testCheckoutMethodNotEnabled(t *testing.T) {
	b := newBuyer()
	h.addToCart(t, b, h.fake.AddSku(catalogfake.NewShop("Stripe Only"), 10_000, 10).GetSkuId(), 1)

	status, body := h.checkout(t, b, "ck-stripe", checkoutBodyWith("STRIPE"))
	require.Equal(t, http.StatusBadRequest, status, string(body))
	e := decode[errResp](t, body)
	assert.Equal(t, "VALIDATION_ERROR", e.Error.Code)
	fields, ok := e.Error.Details["fields"].(map[string]any)
	require.True(t, ok, "details.fields present: %v", e.Error.Details)
	assert.Contains(t, fields, "payment_method")

	status, body = h.do(t, b, http.MethodGet, "/api/orders", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Empty(t, decode[pageResp](t, body).Items, "the rejected checkout created no order")

	// The rejected request never claimed the key: the same key now works.
	status, body = h.checkout(t, b, "ck-stripe", checkoutBody())
	require.Equal(t, http.StatusCreated, status, string(body))
}

func (h *harness) testCheckoutOutOfStock(t *testing.T) {
	b := newBuyer()
	a := h.fake.AddSku(catalogfake.NewShop("Plenty"), 10_000, 10)
	short := h.fake.AddSku(catalogfake.NewShop("Scarce"), 10_000, 1)
	h.addToCart(t, b, a.GetSkuId(), 2)
	h.addToCart(t, b, short.GetSkuId(), 3)

	status, body := h.checkout(t, b, "oos", checkoutBody())
	require.Equal(t, http.StatusConflict, status, string(body))
	e := decode[errResp](t, body)
	assert.Equal(t, "OUT_OF_STOCK", e.Error.Code)
	assert.Equal(t, []any{map[string]any{"sku_id": short.GetSkuId(), "requested": 3.0, "available": 1.0}}, e.Error.Details["shortages"])

	status, body = h.do(t, b, http.MethodGet, "/api/orders?status=CANCELLED", nil)
	require.Equal(t, http.StatusOK, status)
	page := decode[pageResp](t, body)
	require.Len(t, page.Items, 2)
	for _, o := range page.Items {
		assert.Equal(t, "OUT_OF_STOCK", *o.CancelReason)
		assert.Contains(t, h.fake.ReleasedOrders(), o.ID)
		h.events.waitTypes(t, o.ID, event.TypeOrderStatusChanged, event.TypeOrderCancelled)
	}
	assert.Equal(t, int32(10), h.fake.Stock(a.GetSkuId()))
	status, body = h.do(t, b, http.MethodGet, "/api/cart", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Len(t, decode[cartResp](t, body).Shops, 2, "the cart is kept")
}

func (h *harness) testCheckoutCatalogDownThenRetry(t *testing.T) {
	b := newBuyer()
	s := h.fake.AddSku(catalogfake.NewShop("Flaky"), 10_000, 10)
	h.addToCart(t, b, s.GetSkuId(), 1)
	h.fake.FailReserveForSkus(codes.Unavailable, s.GetSkuId())

	status, body := h.checkout(t, b, "flaky", checkoutBody())
	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Equal(t, "UPSTREAM_UNAVAILABLE", decode[errResp](t, body).Error.Code)

	h.fake.FailReserveForSkus(codes.OK, s.GetSkuId())
	status, body = h.checkout(t, b, "flaky", checkoutBody())
	assert.Equal(t, http.StatusCreated, status, string(body))
}

func (h *harness) testCheckoutEmptyCart(t *testing.T) {
	status, body := h.checkout(t, newBuyer(), "empty", checkoutBody())
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Equal(t, "VALIDATION_ERROR", decode[errResp](t, body).Error.Code)
}

func (h *harness) testEventsValid(t *testing.T) {
	h.events.mu.Lock()
	defer h.events.mu.Unlock()
	assert.Positive(t, h.events.total)
	assert.Empty(t, h.events.invalid)
}
