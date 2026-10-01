package itest

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	paymentv1 "github.com/ngoxuanchien/e-commerce/gen/payment/v1"
	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/catalog/catalogfake"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/bootstrap"
)

// onlineCheckout checks out one SKU of each shop with VNPAY through s and
// returns the buyer, the checkout ID and its orders.
func (h *harness) onlineCheckout(t *testing.T, s *harness, key string, shops ...catalogfake.Shop) (caller, string, []orderResp) {
	t.Helper()
	b := newBuyer()
	for _, shop := range shops {
		s.addToCart(t, b, h.fake.AddSku(shop, 50_000, 10).GetSkuId(), 1)
	}
	status, body := s.checkout(t, b, key, checkoutBodyWith("VNPAY"))
	require.Equal(t, http.StatusCreated, status, string(body))
	res := decode[checkoutResp](t, body)
	require.Len(t, res.Orders, len(shops))
	for _, o := range res.Orders {
		require.Equal(t, "AWAITING_PAYMENT", o.Status)
	}
	return b, res.CheckoutID, res.Orders
}

// testPaymentTimeout: an online checkout that never pays is cancelled whole,
// its stock released and an OrderCancelled published for every order.
func (h *harness) testPaymentTimeout(t *testing.T) {
	s := h.scoped(t, func(c *bootstrap.Config) { c.PaymentTimeout = time.Second })
	b, checkoutID, orders := h.onlineCheckout(t, s, "ck-timeout",
		catalogfake.NewShop("Timeout A"), catalogfake.NewShop("Timeout B"))
	h.pay.SetOutcome(checkoutID, paymentv1.Outcome_OUTCOME_NOT_PAID)

	require.Eventually(t, func() bool {
		for _, o := range orders {
			status, body, err := h.try(h.contract, b, http.MethodGet, "/api/orders/"+o.ID, nil)
			if err != nil || status != http.StatusOK {
				return false
			}
			got := decode[orderResp](t, body)
			if got.Status != "CANCELLED" {
				return false
			}
		}
		return true
	}, 10*time.Second, 100*time.Millisecond)

	for _, o := range orders {
		status, body := h.do(t, b, http.MethodGet, "/api/orders/"+o.ID, nil)
		require.Equal(t, http.StatusOK, status)
		got := decode[orderResp](t, body)
		assert.Equal(t, "PAYMENT_TIMEOUT", *got.CancelReason)
		assert.Equal(t, "system", got.History[len(got.History)-1].ActorRole)
		h.events.waitTypes(t, o.ID, event.TypeOrderCreated, event.TypeOrderStatusChanged,
			event.TypeOrderStatusChanged, event.TypeOrderCancelled)
	}
	released := h.fake.ReleasedOrders()
	for _, o := range orders {
		assert.Contains(t, released, o.ID, "the reservation of a timed-out order goes back to stock")
	}
}

// testPaymentTimeoutPaidUntouched: payment-svc says PAID, so the sweeper must
// not cancel the checkout, however late it is.
func (h *harness) testPaymentTimeoutPaidUntouched(t *testing.T) {
	s := h.scoped(t, func(c *bootstrap.Config) { c.PaymentTimeout = time.Second })
	b, checkoutID, orders := h.onlineCheckout(t, s, "ck-paid", catalogfake.NewShop("Paid"))
	h.pay.SetOutcome(checkoutID, paymentv1.Outcome_OUTCOME_PAID)

	require.Eventually(t, func() bool { return h.pay.Calls(checkoutID) >= 2 },
		10*time.Second, 100*time.Millisecond, "the sweeper keeps asking about the paid checkout")

	for _, o := range orders {
		status, body := h.do(t, b, http.MethodGet, "/api/orders/"+o.ID, nil)
		require.Equal(t, http.StatusOK, status)
		assert.Equal(t, "AWAITING_PAYMENT", decode[orderResp](t, body).Status)
	}
}

// testPaymentTimeoutUnavailableThenNotPaid: while payment-svc is down the
// checkout survives; once it answers NOT_PAID the next tick cancels it.
func (h *harness) testPaymentTimeoutUnavailableThenNotPaid(t *testing.T) {
	s := h.scoped(t, func(c *bootstrap.Config) { c.PaymentTimeout = time.Second })
	b, checkoutID, orders := h.onlineCheckout(t, s, "ck-unavailable", catalogfake.NewShop("Unavailable"))
	h.pay.SetError(checkoutID, codes.Unavailable)

	require.Eventually(t, func() bool { return h.pay.Calls(checkoutID) >= 2 },
		10*time.Second, 100*time.Millisecond, "the sweeper retries the unavailable service")
	for _, o := range orders {
		status, body := h.do(t, b, http.MethodGet, "/api/orders/"+o.ID, nil)
		require.Equal(t, http.StatusOK, status)
		assert.Equal(t, "AWAITING_PAYMENT", decode[orderResp](t, body).Status, "untouched while payment-svc is down")
	}

	h.pay.SetOutcome(checkoutID, paymentv1.Outcome_OUTCOME_NOT_PAID)
	require.Eventually(t, func() bool {
		status, body, err := h.try(h.contract, b, http.MethodGet, "/api/orders/"+orders[0].ID, nil)
		return err == nil && status == http.StatusOK && decode[orderResp](t, body).Status == "CANCELLED"
	}, 10*time.Second, 100*time.Millisecond)
}

// testAutoComplete: a delivered order the buyer never confirms is completed
// by the system after the auto-complete deadline, with OrderCompleted.
func (h *harness) testAutoComplete(t *testing.T) {
	s := h.scoped(t, func(c *bootstrap.Config) { c.AutoCompleteAfter = time.Second })
	b, shop := newBuyer(), catalogfake.NewShop("Auto Complete")
	s.addToCart(t, b, h.fake.AddSku(shop, 50_000, 10).GetSkuId(), 1)
	status, body := s.checkout(t, b, "ck-auto", checkoutBody())
	require.Equal(t, http.StatusCreated, status, string(body))
	o := decode[checkoutResp](t, body).Orders[0]

	seller := sellerOf(shop)
	for _, step := range []struct {
		path string
		body any
	}{
		{"/api/seller/orders/" + o.ID + "/confirm", nil},
		{"/api/seller/orders/" + o.ID + "/ship", map[string]string{"reason": "GHN-9"}},
		{"/api/seller/orders/" + o.ID + "/deliver", nil},
	} {
		status, _ := s.act(t, seller, step.path, step.body)
		require.Equal(t, http.StatusOK, status, step.path)
	}
	status, body = s.do(t, b, http.MethodGet, "/api/orders/"+o.ID, nil)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "DELIVERED", decode[orderResp](t, body).Status)

	require.Eventually(t, func() bool {
		status, body, err := h.try(h.contract, b, http.MethodGet, "/api/orders/"+o.ID, nil)
		return err == nil && status == http.StatusOK && decode[orderResp](t, body).Status == "COMPLETED"
	}, 10*time.Second, 100*time.Millisecond)

	status, body = h.do(t, b, http.MethodGet, "/api/orders/"+o.ID, nil)
	require.Equal(t, http.StatusOK, status)
	got := decode[orderResp](t, body)
	assert.Equal(t, "COMPLETED", got.Status)
	assert.Equal(t, "system", got.History[len(got.History)-1].ActorRole)
	require.NotNil(t, got.DeliveredAt, "delivered_at is kept on a completed order")

	evs := h.events.waitTypes(t, o.ID, event.TypeOrderCreated, event.TypeOrderStatusChanged,
		event.TypeOrderStatusChanged, event.TypeOrderStatusChanged, event.TypeOrderStatusChanged,
		event.TypeOrderStatusChanged, event.TypeOrderCompleted)
	completed, err := event.DecodeData[event.OrderCompleted](evs[len(evs)-1])
	require.NoError(t, err)
	assert.Equal(t, "system", completed.CompletedBy)
	assert.Equal(t, o.CheckoutID, completed.CheckoutID)
	assert.Equal(t, "COD", completed.PaymentMethod)
}
