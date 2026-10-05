package itest

import (
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	orderv1 "github.com/ngoxuanchien/e-commerce/gen/order/v1"
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

// paySucceeded builds and publishes the PaymentSucceeded of res: one
// allocation per order, carrying that order's seller, total and commission.
// The shops are keyed by shop ID so the allocation names the right seller.
func (h *harness) paySucceeded(t *testing.T, eventID string, res checkoutResp, shops map[string]catalogfake.Shop) {
	t.Helper()
	paymentID := newID()
	ev := event.PaymentSucceeded{
		PaymentID: paymentID, CheckoutID: res.CheckoutID,
		Provider: event.ProviderVNPay, ProviderRef: "vnpay-" + paymentID,
		Allocations: make([]event.Allocation, 0, len(res.Orders)),
	}
	for _, o := range res.Orders {
		shop, ok := shops[o.Shop.ID]
		require.True(t, ok, "no fake shop for %s", o.Shop.ID)
		ev.Amount += o.Total
		ev.Allocations = append(ev.Allocations, event.Allocation{
			OrderID: o.ID, SellerUserID: shop.SellerUserID,
			Amount: o.Total, CommissionAmount: o.CommissionAmount,
		})
	}
	h.publishPayment(t, eventID, res.CheckoutID, ev)
}

// eventuallyOrder polls the order of c until match accepts it; it returns the
// last response it saw, so a caller can assert on the fields it waited for.
func (h *harness) eventuallyOrder(t *testing.T, c caller, orderID string, match func(orderResp) bool, what string) orderResp {
	t.Helper()
	var got orderResp
	require.Eventually(t, func() bool {
		status, body, err := h.try(h.contract, c, http.MethodGet, "/api/orders/"+orderID, nil)
		if err != nil || status != http.StatusOK {
			return false
		}
		got = decode[orderResp](t, body)
		return match(got)
	}, 15*time.Second, 100*time.Millisecond, "order %s never reached %s", orderID, what)
	return got
}

// isPaid is an order whose payment was captured: confirmed and paid.
func isPaid(o orderResp) bool { return o.Status == "PENDING_CONFIRMATION" && o.PaymentStatus == "PAID" }

// isCancelledAndRefunding is an order cancelled before its payment arrived and
// since captured, so its money has to go back.
func isCancelledAndRefunding(o orderResp) bool {
	return o.Status == "CANCELLED" && o.PaymentStatus == "REFUND_PENDING"
}

// processedEvent waits until the consumer recorded eventID in the inbox.
func (h *harness) processedEvent(t *testing.T, eventID string) {
	t.Helper()
	require.Eventually(t, func() bool {
		var ok bool
		return h.pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM processed_events WHERE event_id = $1)`, eventID).Scan(&ok) == nil && ok
	}, 15*time.Second, 50*time.Millisecond, "payment event %s was never applied", eventID)
}

// testPaymentSucceeded: a captured payment moves every allocated order to
// PENDING_CONFIRMATION/PAID, and redelivering the event changes nothing.
func (h *harness) testPaymentSucceeded(t *testing.T) {
	shops := map[string]catalogfake.Shop{}
	var list []catalogfake.Shop
	for _, name := range []string{"Paid A", "Paid B"} {
		shop := catalogfake.NewShop(name)
		shops[shop.ID] = shop
		list = append(list, shop)
	}
	b, checkoutID, orders := h.onlineCheckout(t, h, "ck-paid-succeeded", list...)
	res := checkoutResp{CheckoutID: checkoutID, Orders: orders}

	eventID := newID()
	h.paySucceeded(t, eventID, res, shops)
	for _, o := range orders {
		h.eventuallyOrder(t, b, o.ID, isPaid, "PENDING_CONFIRMATION/PAID")
	}
	h.processedEvent(t, eventID)

	// Redeliver the same event, then wait for a later marker record on the same
	// checkout key: both records are on one partition, so once the marker's
	// inbox row exists the redelivery has been consumed too.
	h.paySucceeded(t, eventID, res, shops)
	marker := newID()
	h.publishPayment(t, marker, checkoutID, event.PaymentSucceeded{
		PaymentID: newID(), CheckoutID: checkoutID, Provider: event.ProviderVNPay, Amount: 0,
		Allocations: []event.Allocation{},
	})
	h.processedEvent(t, marker)

	for _, o := range orders {
		status, body := h.do(t, b, http.MethodGet, "/api/orders/"+o.ID, nil)
		require.Equal(t, http.StatusOK, status)
		got := decode[orderResp](t, body)
		paid := 0
		for _, e := range got.History {
			if e.To == "PENDING_CONFIRMATION" {
				paid++
			}
		}
		assert.Equal(t, 1, paid, "the redelivered payment is applied once")
		h.events.waitTypes(t, o.ID, event.TypeOrderCreated, event.TypeOrderStatusChanged,
			event.TypeOrderStatusChanged)
	}
}

// testLateSuccessThenRefund: a capture that arrives after the buyer cancelled
// leaves the money REFUND_PENDING; refunding one order of the checkout leaves
// the other waiting for its own refund.
func (h *harness) testLateSuccessThenRefund(t *testing.T) {
	shopA, shopB := catalogfake.NewShop("Late A"), catalogfake.NewShop("Late B")
	shops := map[string]catalogfake.Shop{shopA.ID: shopA, shopB.ID: shopB}
	b, checkoutID, orders := h.onlineCheckout(t, h, "ck-late-refund", shopA, shopB)
	first, second := orders[0], orders[1]

	status, _ := h.act(t, b, "/api/orders/"+first.ID+"/cancel", map[string]string{"reason": "changed my mind"})
	require.Equal(t, http.StatusOK, status)

	h.paySucceeded(t, newID(), checkoutResp{CheckoutID: checkoutID, Orders: orders}, shops)
	for _, o := range orders {
		h.eventuallyOrder(t, b, o.ID, isCancelledAndRefunding, "CANCELLED/REFUND_PENDING")
	}

	h.publishPayment(t, newID(), checkoutID, event.PaymentRefunded{
		RefundID: newID(), PaymentID: newID(), CheckoutID: checkoutID,
		OrderID: &first.ID, Amount: first.Total, Provider: event.ProviderVNPay,
	})
	require.Eventually(t, func() bool {
		status, body := h.do(t, b, http.MethodGet, "/api/orders/"+first.ID, nil)
		if status != http.StatusOK {
			return false
		}
		return decode[orderResp](t, body).PaymentStatus == "REFUNDED"
	}, 15*time.Second, 100*time.Millisecond, "the refunded order")

	status, body := h.do(t, b, http.MethodGet, "/api/orders/"+second.ID, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "REFUND_PENDING", decode[orderResp](t, body).PaymentStatus,
		"only the refunded order's money moved")
}

// testPaidOrderCancelledBySeller: a paid order the seller cancels becomes
// REFUND_PENDING, and the OrderCancelled carries the checkout and method, so
// ledger-svc can refund it.
func (h *harness) testPaidOrderCancelledBySeller(t *testing.T) {
	shop := catalogfake.NewShop("Paid Cancel")
	shops := map[string]catalogfake.Shop{shop.ID: shop}
	b, checkoutID, orders := h.onlineCheckout(t, h, "ck-paid-cancel", shop)
	o := orders[0]

	h.paySucceeded(t, newID(), checkoutResp{CheckoutID: checkoutID, Orders: orders}, shops)
	h.eventuallyOrder(t, b, o.ID, isPaid, "PENDING_CONFIRMATION/PAID")

	seller := sellerOf(shop)
	status, _ := h.act(t, seller, "/api/seller/orders/"+o.ID+"/confirm", nil)
	require.Equal(t, http.StatusOK, status)
	status, got := h.act(t, seller, "/api/seller/orders/"+o.ID+"/cancel", map[string]string{"reason": "out of stock"})
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "CANCELLED", got.Status)
	assert.Equal(t, "REFUND_PENDING", got.PaymentStatus)

	// OrderCreated, await_payment, mark_paid, confirm, cancel.
	evs := h.events.waitTypes(t, o.ID, event.TypeOrderCreated, event.TypeOrderStatusChanged,
		event.TypeOrderStatusChanged, event.TypeOrderStatusChanged, event.TypeOrderStatusChanged,
		event.TypeOrderCancelled)
	cancelled, err := event.DecodeData[event.OrderCancelled](evs[len(evs)-1])
	require.NoError(t, err)
	assert.Equal(t, event.OrderCancelled{
		OrderID: o.ID, Reason: "out of stock", CancelledBy: "seller",
		CheckoutID: checkoutID, PaymentMethod: "VNPAY",
	}, cancelled)
}

// testPaymentTimeout: an online checkout that never pays is cancelled whole,
// its stock released and an OrderCancelled published for every order.
func (h *harness) testPaymentTimeout(t *testing.T) {
	s := h.scoped(t, func(c *bootstrap.Config) { c.PaymentTimeout = time.Second })
	b, checkoutID, orders := h.onlineCheckout(t, s, "ck-timeout",
		catalogfake.NewShop("Timeout A"), catalogfake.NewShop("Timeout B"))
	s.pay.SetOutcome(checkoutID, paymentv1.Outcome_OUTCOME_NOT_PAID)

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
	s.pay.SetOutcome(checkoutID, paymentv1.Outcome_OUTCOME_PAID)

	require.Eventually(t, func() bool { return s.pay.Calls(checkoutID) >= 2 },
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
	s.pay.SetError(checkoutID, codes.Unavailable)

	require.Eventually(t, func() bool { return s.pay.Calls(checkoutID) >= 2 },
		10*time.Second, 100*time.Millisecond, "the sweeper retries the unavailable service")
	for _, o := range orders {
		status, body := h.do(t, b, http.MethodGet, "/api/orders/"+o.ID, nil)
		require.Equal(t, http.StatusOK, status)
		assert.Equal(t, "AWAITING_PAYMENT", decode[orderResp](t, body).Status, "untouched while payment-svc is down")
	}

	s.pay.SetOutcome(checkoutID, paymentv1.Outcome_OUTCOME_NOT_PAID)
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
	var delivered orderResp
	for _, step := range []struct {
		path string
		body any
	}{
		{"/api/seller/orders/" + o.ID + "/confirm", nil},
		{"/api/seller/orders/" + o.ID + "/ship", map[string]string{"reason": "GHN-9"}},
		{"/api/seller/orders/" + o.ID + "/deliver", nil},
	} {
		var status int
		status, delivered = s.act(t, seller, step.path, step.body)
		require.Equal(t, http.StatusOK, status, step.path)
	}
	// The deliver response itself proves DELIVERED: a separate GET could
	// already see COMPLETED, because the sweeper may complete the order any
	// time after one second (a tight window under -race).
	require.Equal(t, "DELIVERED", delivered.Status)
	require.NotNil(t, delivered.DeliveredAt, "deliver stamps delivered_at")

	require.Eventually(t, func() bool {
		status, body, err := h.try(h.contract, b, http.MethodGet, "/api/orders/"+o.ID, nil)
		return err == nil && status == http.StatusOK && decode[orderResp](t, body).Status == "COMPLETED"
	}, 20*time.Second, 100*time.Millisecond,
		"order %s was not auto-completed within 20 s of its delivery (AUTO_COMPLETE_AFTER = 1 s, sweeper every %v)", o.ID, sweeperInterval)

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

// testGetCheckout: the OrderInternal gRPC API answers what payment-svc needs to
// allocate one payment across a checkout — the buyer, the method, the deadline
// and every order with the money already split — and answers NOT_FOUND for a
// checkout that does not exist.
func (h *harness) testGetCheckout(t *testing.T) {
	shops := map[string]catalogfake.Shop{}
	var list []catalogfake.Shop
	for _, name := range []string{"Get A", "Get B"} {
		shop := catalogfake.NewShop(name)
		shops[shop.ID] = shop
		list = append(list, shop)
	}
	b, checkoutID, orders := h.onlineCheckout(t, h, "ck-get-checkout", list...)

	resp, err := h.orderRPC.GetCheckout(ctx, &orderv1.GetCheckoutRequest{CheckoutId: checkoutID})
	require.NoError(t, err)
	assert.Equal(t, checkoutID, resp.GetCheckoutId())
	assert.Equal(t, b.id, resp.GetBuyerId())
	assert.Equal(t, "VNPAY", resp.GetPaymentMethod())
	require.NotNil(t, resp.GetPaymentDeadline())
	require.NotNil(t, orders[0].PaymentDeadline)
	wantDeadline, err := time.Parse(time.RFC3339, *orders[0].PaymentDeadline)
	require.NoError(t, err)
	assert.True(t, wantDeadline.Equal(resp.GetPaymentDeadline().AsTime()),
		"the gRPC deadline is the one the REST checkout answered")

	got := resp.GetOrders()
	require.Len(t, got, 2)
	sort.Slice(orders, func(i, j int) bool { return orders[i].ID < orders[j].ID })
	for i, o := range got {
		rest := orders[i]
		assert.Equal(t, rest.ID, o.GetOrderId())
		assert.Equal(t, rest.Status, o.GetStatus())
		assert.Equal(t, shops[rest.Shop.ID].SellerUserID, o.GetSellerUserId())
		assert.Equal(t, rest.Subtotal, o.GetSubtotal())
		assert.Equal(t, rest.Total, o.GetTotal())
		assert.Equal(t, rest.CommissionAmount, o.GetCommissionAmount())
		assert.Positive(t, o.GetCommissionAmount(), "an online order carries commission")
	}

	_, err = h.orderRPC.GetCheckout(ctx, &orderv1.GetCheckoutRequest{CheckoutId: newID()})
	assert.Equal(t, codes.NotFound, status.Code(err))
}
