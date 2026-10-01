package itest

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/pkg/server"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/catalog"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/catalog/catalogfake"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/postgres"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

// placeOrder checks out one SKU of a new shop and returns the order and shop.
func (h *harness) placeOrder(t *testing.T, b caller) (orderResp, catalogfake.Shop) {
	t.Helper()
	shop := catalogfake.NewShop("Life " + newID()[30:])
	s := h.fake.AddSku(shop, 50_000, 10)
	h.addToCart(t, b, s.GetSkuId(), 1)
	status, body := h.checkout(t, b, newID(), checkoutBody(s.GetSkuId()))
	require.Equal(t, http.StatusCreated, status, string(body))
	return decode[checkoutResp](t, body).Orders[0], shop
}

func (h *harness) act(t *testing.T, c caller, path string, body any) (int, orderResp) {
	t.Helper()
	status, b := h.do(t, c, http.MethodPost, path, body)
	if status != http.StatusOK {
		return status, orderResp{}
	}
	return status, decode[orderResp](t, b)
}

func (h *harness) testLifecycle(t *testing.T) {
	b := newBuyer()
	o, shop := h.placeOrder(t, b)
	s := sellerOf(shop)
	for _, step := range []struct {
		who  caller
		path string
		body any
		want string
	}{
		{s, "/api/seller/orders/" + o.ID + "/confirm", nil, "CONFIRMED"},
		{s, "/api/seller/orders/" + o.ID + "/ship", map[string]string{"reason": "GHN-123"}, "SHIPPING"},
		{s, "/api/seller/orders/" + o.ID + "/deliver", nil, "DELIVERED"},
		{b, "/api/orders/" + o.ID + "/complete", nil, "COMPLETED"},
	} {
		status, got := h.act(t, step.who, step.path, step.body)
		require.Equal(t, http.StatusOK, status, step.path)
		assert.Equal(t, step.want, got.Status)
	}
	for _, who := range []caller{b, s} {
		status, body := h.do(t, who, http.MethodGet, "/api/orders/"+o.ID, nil)
		require.Equal(t, http.StatusOK, status)
		got := decode[orderResp](t, body)
		require.Len(t, got.History, 6)
		assert.Equal(t, "GHN-123", *got.History[3].Note)
	}
	evs := h.events.waitTypes(t, o.ID, event.TypeOrderCreated,
		event.TypeOrderStatusChanged, event.TypeOrderStatusChanged, event.TypeOrderStatusChanged,
		event.TypeOrderStatusChanged, event.TypeOrderStatusChanged, event.TypeOrderCompleted)
	completed, err := event.DecodeData[event.OrderCompleted](evs[len(evs)-1])
	require.NoError(t, err)
	assert.Equal(t, event.OrderCompleted{
		OrderID: o.ID, CheckoutID: o.CheckoutID, SellerUserID: shop.SellerUserID, PaymentMethod: "COD",
		Total: o.Total, CommissionAmount: 0, CompletedBy: "buyer",
	}, completed)
}

func (h *harness) testBuyerCannotCancelConfirmed(t *testing.T) {
	b := newBuyer()
	o, shop := h.placeOrder(t, b)
	status, _ := h.act(t, sellerOf(shop), "/api/seller/orders/"+o.ID+"/confirm", nil)
	require.Equal(t, http.StatusOK, status)

	status, body := h.do(t, b, http.MethodPost, "/api/orders/"+o.ID+"/cancel", map[string]string{"reason": "đổi ý"})
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "INVALID_STATE_TRANSITION", decode[errResp](t, body).Error.Code)

	status, got := h.act(t, sellerOf(shop), "/api/seller/orders/"+o.ID+"/cancel", map[string]string{"reason": "hết hàng"})
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "CANCELLED", got.Status)
	evs := h.events.waitTypes(t, o.ID, event.TypeOrderCreated, event.TypeOrderStatusChanged,
		event.TypeOrderStatusChanged, event.TypeOrderStatusChanged, event.TypeOrderCancelled)
	c, err := event.DecodeData[event.OrderCancelled](evs[4])
	require.NoError(t, err)
	assert.Equal(t, event.OrderCancelled{
		OrderID: o.ID, Reason: "hết hàng", CancelledBy: "seller",
		CheckoutID: o.CheckoutID, PaymentMethod: "COD",
	}, c)
}

// testBuyerCancelsAwaitingCheckout cancels one order of an unpaid checkout:
// every order of that checkout still awaiting payment is cancelled with it.
func (h *harness) testBuyerCancelsAwaitingCheckout(t *testing.T) {
	b, shopA, shopB := newBuyer(), catalogfake.NewShop("Cancel A"), catalogfake.NewShop("Cancel B")
	a := h.fake.AddSku(shopA, 120_000, 10)
	bb := h.fake.AddSku(shopB, 45_000, 10)
	h.addToCart(t, b, a.GetSkuId(), 1)
	h.addToCart(t, b, bb.GetSkuId(), 1)

	status, body := h.checkout(t, b, "ck-cancel", checkoutBodyWith("VNPAY"))
	require.Equal(t, http.StatusCreated, status, string(body))
	res := decode[checkoutResp](t, body)
	require.Len(t, res.Orders, 2)
	first, second := res.Orders[0], res.Orders[1]
	require.Equal(t, "AWAITING_PAYMENT", first.Status)
	h.events.waitTypes(t, first.ID, event.TypeOrderCreated, event.TypeOrderStatusChanged)
	h.events.waitTypes(t, second.ID, event.TypeOrderCreated, event.TypeOrderStatusChanged)

	status, got := h.act(t, b, "/api/orders/"+first.ID+"/cancel", map[string]string{"reason": "no longer needed"})
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "CANCELLED", got.Status)
	assert.Equal(t, "no longer needed", *got.CancelReason)
	assert.Equal(t, res.CheckoutID, got.CheckoutID)

	status, body = h.do(t, b, http.MethodGet, "/api/orders/"+second.ID, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "CANCELLED", decode[orderResp](t, body).Status, "the sibling is cancelled too")

	for _, o := range []orderResp{first, second} {
		evs := h.events.waitTypes(t, o.ID, event.TypeOrderCreated, event.TypeOrderStatusChanged,
			event.TypeOrderStatusChanged, event.TypeOrderCancelled)
		c, err := event.DecodeData[event.OrderCancelled](evs[3])
		require.NoError(t, err)
		assert.Equal(t, event.OrderCancelled{
			OrderID: o.ID, Reason: "no longer needed", CancelledBy: "buyer",
			CheckoutID: res.CheckoutID, PaymentMethod: "VNPAY",
		}, c)
		changed, err := event.DecodeData[event.OrderStatusChanged](evs[2])
		require.NoError(t, err)
		assert.Equal(t, event.OrderStatusChanged{
			OrderID: o.ID, From: "AWAITING_PAYMENT", To: "CANCELLED", ActorID: b.id, ActorRole: "buyer",
		}, changed)
	}
}

// testSellerCannotActOnAwaitingPayment: the seller may neither confirm nor
// cancel an unpaid order, but does see it in their list.
func (h *harness) testSellerCannotActOnAwaitingPayment(t *testing.T) {
	b, shop := newBuyer(), catalogfake.NewShop("Await Shop")
	h.addToCart(t, b, h.fake.AddSku(shop, 50_000, 10).GetSkuId(), 1)
	status, body := h.checkout(t, b, "ck-await", checkoutBodyWith("VNPAY"))
	require.Equal(t, http.StatusCreated, status, string(body))
	o := decode[checkoutResp](t, body).Orders[0]
	require.Equal(t, "AWAITING_PAYMENT", o.Status)

	s := sellerOf(shop)
	status, body = h.do(t, s, http.MethodPost, "/api/seller/orders/"+o.ID+"/confirm", nil)
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "INVALID_STATE_TRANSITION", decode[errResp](t, body).Error.Code)
	status, body = h.do(t, s, http.MethodPost, "/api/seller/orders/"+o.ID+"/cancel", map[string]string{"reason": "hết hàng"})
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "INVALID_STATE_TRANSITION", decode[errResp](t, body).Error.Code)

	status, body = h.do(t, s, http.MethodGet, "/api/seller/orders?status=AWAITING_PAYMENT", nil)
	require.Equal(t, http.StatusOK, status, string(body))
	page := decode[pageResp](t, body)
	require.Len(t, page.Items, 1)
	assert.Equal(t, o.ID, page.Items[0].ID)
}

func (h *harness) testStrangers(t *testing.T) {
	o, _ := h.placeOrder(t, newBuyer())
	other := sellerOf(catalogfake.NewShop("Other"))
	status, _ := h.do(t, other, http.MethodPost, "/api/seller/orders/"+o.ID+"/confirm", nil)
	assert.Equal(t, http.StatusNotFound, status)
	status, _ = h.do(t, other, http.MethodGet, "/api/orders/"+o.ID, nil)
	assert.Equal(t, http.StatusNotFound, status)
	status, _ = h.do(t, newBuyer(), http.MethodGet, "/api/seller/orders", nil)
	assert.Equal(t, http.StatusForbidden, status)
}

func (h *harness) testConcurrentConfirmCancel(t *testing.T) {
	for range 10 {
		b := newBuyer()
		o, shop := h.placeOrder(t, b)
		type call struct {
			c    caller
			path string
		}
		calls := []call{{sellerOf(shop), "/api/seller/orders/" + o.ID + "/confirm"}, {b, "/api/orders/" + o.ID + "/cancel"}}
		statuses := make([]int, len(calls))
		errs := make([]error, len(calls))
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i, c := range calls {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				statuses[i], _, errs[i] = h.try(h.contract, c.c, http.MethodPost, c.path, strings.NewReader(`{"reason":"đổi ý"}`))
			}()
		}
		close(start)
		wg.Wait()
		for _, err := range errs {
			require.NoError(t, err)
		}
		assert.ElementsMatch(t, []int{http.StatusOK, http.StatusConflict}, statuses)
	}
}

func (h *harness) testUppercaseIDs(t *testing.T) {
	b := newBuyer()
	o, _ := h.placeOrder(t, b)
	upper := caller{id: strings.ToUpper(b.id), roles: b.roles}
	status, body := h.doRaw(t, upper, http.MethodGet, "/api/orders/"+strings.ToUpper(o.ID), "")
	require.Equal(t, http.StatusOK, status, string(body))
	assert.Equal(t, o.ID, decode[orderResp](t, body).ID)
}

func (h *harness) testPagination(t *testing.T) {
	b := newBuyer()
	shops := []catalogfake.Shop{catalogfake.NewShop("P1"), catalogfake.NewShop("P2"), catalogfake.NewShop("P3")}
	for _, s := range shops {
		h.addToCart(t, b, h.fake.AddSku(s, 10_000, 5).GetSkuId(), 1)
	}
	status, body := h.checkout(t, b, "page", checkoutBody())
	require.Equal(t, http.StatusCreated, status, string(body))

	status, body = h.do(t, b, http.MethodGet, "/api/orders?limit=2", nil)
	require.Equal(t, http.StatusOK, status)
	first := decode[pageResp](t, body)
	require.Len(t, first.Items, 2)
	require.NotNil(t, first.NextCursor)

	status, body = h.do(t, b, http.MethodGet, "/api/orders?limit=2&cursor="+*first.NextCursor, nil)
	require.Equal(t, http.StatusOK, status)
	second := decode[pageResp](t, body)
	require.Len(t, second.Items, 1)
	assert.Nil(t, second.NextCursor)
	seen := map[string]bool{first.Items[0].ID: true, first.Items[1].ID: true, second.Items[0].ID: true}
	assert.Len(t, seen, 3, "no overlap between pages")

	status, body = h.do(t, sellerOf(shops[0]), http.MethodGet, "/api/seller/orders?status=PENDING_CONFIRMATION", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Len(t, decode[pageResp](t, body).Items, 1)
}

func (h *harness) testSweeperCancelsCrashedCheckout(t *testing.T) {
	// Simulate a crash between Tx A and Tx C: a PENDING order holding stock.
	s := h.fake.AddSku(catalogfake.NewShop("Crash"), 10_000, 5)
	cat := catalog.New(h.fake.Dial(t), catalog.DefaultConfig())
	skus, err := cat.GetSkus(ctx, []string{s.GetSkuId()})
	require.NoError(t, err)
	orders, err := domain.BuildOrders(domain.CheckoutParams{
		CheckoutID: newID(), BuyerID: newID(), Lines: []domain.Line{{SkuID: s.GetSkuId(), Quantity: 2}}, Skus: skus,
		ShippingFee: 30_000, Address: domain.Address{FullName: "A", Phone: "1", Line1: "l", Ward: "w", District: "d", Province: "p"},
		PaymentMethod: domain.PaymentCOD, Now: time.Now().UTC().Truncate(time.Microsecond), NewID: newID,
	})
	require.NoError(t, err)
	o := orders[0]
	store := postgres.NewStore(h.pool)
	require.NoError(t, store.InTx(ctx, func(tx app.Tx) error { return tx.InsertOrder(ctx, o) }))
	require.NoError(t, cat.ReserveStock(ctx, o.ID, o.StockLines()))

	buyer := caller{id: o.BuyerID, roles: "buyer"}
	var got orderResp
	require.Eventually(t, func() bool {
		status, body, err := h.try(h.contract, buyer, http.MethodGet, "/api/orders/"+o.ID, nil)
		if err != nil || status != http.StatusOK {
			return false
		}
		got = decode[orderResp](t, body)
		return got.Status == "CANCELLED"
	}, pendingTimeout+sweeperInterval+2*time.Second, 50*time.Millisecond)

	cancelledAt, err := time.Parse(time.RFC3339Nano, got.History[len(got.History)-1].CreatedAt)
	require.NoError(t, err)
	lifetime := cancelledAt.Sub(o.CreatedAt)
	assert.LessOrEqual(t, lifetime, pendingTimeout+sweeperInterval+300*time.Millisecond, "PENDING_TIMEOUT + one interval (+ slack)")
	assert.Equal(t, "CHECKOUT_TIMEOUT", *got.CancelReason)
	assert.Equal(t, int32(5), h.fake.Stock(s.GetSkuId()))
	h.events.waitTypes(t, o.ID, event.TypeOrderStatusChanged, event.TypeOrderCancelled)
}

func (h *harness) testReadiness(t *testing.T) {
	checks := h.app.ReadinessChecks()
	names := make([]string, 0, len(checks))
	for name := range checks {
		names = append(names, name)
	}
	assert.ElementsMatch(t, []string{"postgres", "redis", "kafka"}, names)
	rec := httptest.NewRecorder()
	server.Readyz(checks).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}
