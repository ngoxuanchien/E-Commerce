package itest

import (
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	paymentv1 "github.com/ngoxuanchien/e-commerce/gen/payment/v1"
	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/pkg/kafkax"
)

// payFresh creates a payment for a new checkout, pays it on the FAKE page and returns the payment
// once PaymentSucceeded is on Kafka.
func (h *harness) payFresh(t *testing.T, method string, hooks url.Values, totals ...int64) (checkout, paymentResp) {
	t.Helper()
	co := h.newCheckout(method, time.Now().Add(time.Hour), totals...)
	p := h.createPayment(t, co.GetBuyerId(), co.GetCheckoutId())
	h.fakeSubmit(t, p.ID, "succeed", hooks)
	h.waitStatus(t, co.GetBuyerId(), p.ID, "SUCCEEDED")
	return co, p
}

func (h *harness) testPaySucceeds(t *testing.T) {
	co := h.newCheckout("STRIPE", time.Now().Add(time.Hour), 130_000, 80_000)
	buyer := co.GetBuyerId()

	p := h.createPayment(t, buyer, co.GetCheckoutId())
	assert.Equal(t, "PENDING", p.Status)
	assert.Equal(t, "STRIPE", p.Method)
	assert.Equal(t, "fake", p.Provider)
	assert.EqualValues(t, 210_000, p.Amount)
	require.NotNil(t, p.RedirectURL)
	assert.Contains(t, *p.RedirectURL, "/api/payments/fake/checkout/"+p.ID)

	h.fakePage(t, p.ID, nil)
	h.fakeSubmit(t, p.ID, "succeed", nil)
	h.waitStatus(t, buyer, p.ID, "SUCCEEDED")

	evs := h.events.waitCount(t, co.GetCheckoutId(), 1)
	require.Len(t, evs, 1)
	require.Equal(t, event.TypePaymentSucceeded, evs[0].EventType)
	got := data[event.PaymentSucceeded](t, evs[0])
	assert.Equal(t, p.ID, got.PaymentID)
	assert.Equal(t, co.GetCheckoutId(), got.CheckoutID)
	assert.Equal(t, buyer, got.BuyerID)
	assert.Equal(t, "fake", got.Provider)
	assert.EqualValues(t, 210_000, got.Amount)
	var want []event.Allocation
	for _, o := range co.GetOrders() {
		want = append(want, event.Allocation{
			OrderID: o.GetOrderId(), SellerUserID: o.GetSellerUserId(), Amount: o.GetTotal(), CommissionAmount: o.GetCommissionAmount(),
		})
	}
	assert.ElementsMatch(t, want, got.Allocations)
}

func (h *harness) testDuplicateWebhook(t *testing.T) {
	co, p := h.payFresh(t, "VNPAY", url.Values{"hook_webhook": {"duplicate"}}, 100_000)
	evs := h.events.waitCount(t, co.GetCheckoutId(), 1)
	require.Equal(t, event.TypePaymentSucceeded, evs[0].EventType)
	assert.Equal(t, p.ID, data[event.PaymentSucceeded](t, evs[0]).PaymentID)
	// The duplicate is delivered with the same event id right after the first one.
	h.events.stays(t, co.GetCheckoutId(), 1, 1500*time.Millisecond)
}

func (h *harness) testStaleAfter(t *testing.T) {
	co, p := h.payFresh(t, "STRIPE", url.Values{"hook_webhook": {"stale_after"}}, 100_000)
	h.events.waitCount(t, co.GetCheckoutId(), 1)
	// The stale "pending" observation follows the success; it must not move the payment.
	h.events.stays(t, co.GetCheckoutId(), 1, 1500*time.Millisecond)
	assert.Equal(t, "SUCCEEDED", h.getPayment(t, co.GetBuyerId(), p.ID).Status)
}

// A provider that cannot cancel (hook_cancel=unsupported) leaves the first attempt payable after its
// window. A second attempt retires it (EXPIRED); when the old page is paid anyway, that payment is
// a DUPLICATE and is refunded in full with order_id null.
func (h *harness) testLateDuplicateRefund(t *testing.T) {
	deadline := time.Now().Add(2 * time.Second)
	co := h.newCheckout("VNPAY", deadline, 120_000)
	buyer := co.GetBuyerId()

	first := h.createPayment(t, buyer, co.GetCheckoutId())
	h.fakePage(t, first.ID, url.Values{"hook_cancel": {"unsupported"}})

	time.Sleep(time.Until(deadline) + 200*time.Millisecond) // the first attempt's window ends
	h.setDeadline(co, time.Now().Add(time.Hour))

	second := h.createPayment(t, buyer, co.GetCheckoutId())
	require.NotEqual(t, first.ID, second.ID)
	assert.Equal(t, "EXPIRED", h.getPayment(t, buyer, first.ID).Status, "creating the second attempt retires the first")

	h.fakeSubmit(t, second.ID, "succeed", nil)
	h.waitStatus(t, buyer, second.ID, "SUCCEEDED")

	h.fakeSubmit(t, first.ID, "succeed", nil) // the buyer paid the old page too
	h.waitStatus(t, buyer, first.ID, "DUPLICATE")

	evs := h.events.waitCount(t, co.GetCheckoutId(), 3)
	require.Len(t, evs, 3)
	var succeeded, refunded []event.Envelope
	for _, e := range evs {
		switch e.EventType {
		case event.TypePaymentSucceeded:
			succeeded = append(succeeded, e)
		case event.TypePaymentRefunded:
			refunded = append(refunded, e)
		default:
			t.Errorf("unexpected event %s", e.EventType)
		}
	}
	require.Len(t, succeeded, 2)
	require.Len(t, refunded, 1)

	for _, e := range succeeded {
		s := data[event.PaymentSucceeded](t, e)
		switch s.PaymentID {
		case second.ID:
			assert.Len(t, s.Allocations, 1)
		case first.ID:
			assert.NotNil(t, s.Allocations)
			assert.Empty(t, s.Allocations, "a DUPLICATE payment carries no allocations")
		default:
			t.Errorf("PaymentSucceeded for unknown payment %s", s.PaymentID)
		}
	}
	r := data[event.PaymentRefunded](t, refunded[0])
	assert.Equal(t, first.ID, r.PaymentID)
	assert.Nil(t, r.OrderID, "a duplicate refund has no order")
	assert.EqualValues(t, 120_000, r.Amount)
	assert.Equal(t, "fake", r.Provider)
	assert.Equal(t, co.GetCheckoutId(), r.CheckoutID)
}

func (h *harness) testOrderCancelledRefund(t *testing.T) {
	co, p := h.payFresh(t, "VNPAY", nil, 130_000, 80_000)
	h.events.waitCount(t, co.GetCheckoutId(), 1)

	cancelled := co.GetOrders()[1]
	val, err := event.Encode(event.OrderCancelled{
		OrderID: cancelled.GetOrderId(), Reason: "buyer changed their mind", CancelledBy: event.CancelledByBuyer,
		CheckoutID: co.GetCheckoutId(), PaymentMethod: event.PaymentMethodVNPay,
	}, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, h.producer.Publish(ctx, kafkax.Message{Topic: event.TopicOrderEvents, Key: []byte(cancelled.GetOrderId()), Value: val}))

	evs := h.events.waitCount(t, co.GetCheckoutId(), 2)
	require.Len(t, evs, 2)
	require.Equal(t, event.TypePaymentRefunded, evs[1].EventType)
	r := data[event.PaymentRefunded](t, evs[1])
	require.NotNil(t, r.OrderID)
	assert.Equal(t, cancelled.GetOrderId(), *r.OrderID)
	assert.Equal(t, p.ID, r.PaymentID)
	assert.EqualValues(t, 80_000, r.Amount)
	assert.Equal(t, "fake", r.Provider)
	h.events.stays(t, co.GetCheckoutId(), 2, time.Second) // the other order is not refunded
}

// A PENDING FAKE attempt whose fake_sessions row is gone cannot be paid: the create that follows must
// retire it as EXPIRED and open a new one, not answer 503.
func (h *harness) testMissingSessionRetry(t *testing.T) {
	co := h.newCheckout("VNPAY", time.Now().Add(time.Hour), 90_000)
	buyer := co.GetBuyerId()

	first := h.createPayment(t, buyer, co.GetCheckoutId())
	_, err := h.pool.Exec(ctx, `DELETE FROM fake_sessions WHERE payment_id = $1`, first.ID)
	require.NoError(t, err)
	require.Equal(t, "PENDING", h.getPayment(t, buyer, first.ID).Status)

	second := h.createPayment(t, buyer, co.GetCheckoutId())
	require.NotEqual(t, first.ID, second.ID)
	assert.Equal(t, "PENDING", second.Status)
	assert.Equal(t, "EXPIRED", h.getPayment(t, buyer, first.ID).Status, "the first attempt is retired")

	h.fakeSubmit(t, second.ID, "succeed", nil)
	h.waitStatus(t, buyer, second.ID, "SUCCEEDED")
}

func (h *harness) testCancelCheckoutPayments(t *testing.T) {
	cancel := func(checkoutID string) (paymentv1.Outcome, error) {
		resp, err := h.grpc.CancelCheckoutPayments(ctx, &paymentv1.CancelCheckoutPaymentsRequest{CheckoutId: checkoutID})
		return resp.GetOutcome(), err
	}

	// An open attempt that the provider can cancel: nothing was paid, and the attempt is closed.
	open := h.newCheckout("STRIPE", time.Now().Add(time.Hour), 90_000)
	p := h.createPayment(t, open.GetBuyerId(), open.GetCheckoutId())
	out, err := cancel(open.GetCheckoutId())
	require.NoError(t, err)
	assert.Equal(t, paymentv1.Outcome_OUTCOME_NOT_PAID, out)
	assert.Equal(t, "EXPIRED", h.getPayment(t, open.GetBuyerId(), p.ID).Status)

	// A paid checkout.
	paid, _ := h.payFresh(t, "VNPAY", nil, 90_000)
	out, err = cancel(paid.GetCheckoutId())
	require.NoError(t, err)
	assert.Equal(t, paymentv1.Outcome_OUTCOME_PAID, out)

	// A checkout payment-svc never saw.
	out, err = cancel(newID())
	require.NoError(t, err)
	assert.Equal(t, paymentv1.Outcome_OUTCOME_NOT_PAID, out)

	// A malformed id never reaches the use case.
	_, err = cancel("not-a-uuid")
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func (h *harness) testReadiness(t *testing.T) {
	checks := h.app.ReadinessChecks()
	require.Len(t, checks, 2)
	for _, name := range []string{"postgres", "kafka"} {
		check, ok := checks[name]
		require.True(t, ok, name)
		assert.NoError(t, check(ctx), name)
	}
}

func (h *harness) testEventsValid(t *testing.T) {
	h.events.mu.Lock()
	defer h.events.mu.Unlock()
	assert.Greater(t, h.events.total, 0, "events were published")
	assert.Empty(t, h.events.invalid, "every event validates against api/events")
}
