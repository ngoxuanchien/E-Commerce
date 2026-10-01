package app_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/app"
)

// TestWorkedExampleThroughEvents is spec §3's example driven by events.
func TestWorkedExampleThroughEvents(t *testing.T) {
	h := newHarness(t)
	c := newCheckout("vnpay", orderSpec{amount: 230000, commission: 10000}, orderSpec{amount: 100000, commission: 3500})
	h.deliver(c.succeeded())
	h.deliver(c.completed(0))
	h.deliver(c.refunded(1))

	assert.Equal(t, int64(230000), h.balance("psp_clearing:vnpay"))
	assert.Equal(t, int64(-220000), h.balance(wallet(c.orders[0].seller)))
	assert.Equal(t, int64(-10000), h.balance("platform_commission_revenue"))
	assert.Zero(t, h.balance("checkout_suspense:"+c.id.String()))
	assert.Zero(t, h.balance(escrow(c.orders[0].id)))
	assert.Zero(t, h.balance(escrow(c.orders[1].id)))
	assert.Zero(t, h.total())
	assert.Equal(t, 4, h.count("journal_transactions")) // PAY, CAPTURE, RELEASE, REFUND
	assert.Equal(t, 2, h.count("escrow_allocations"))
}

func TestRedeliveryIsNoOp(t *testing.T) {
	h := newHarness(t)
	c := newCheckout("fake", orderSpec{amount: 100000, commission: 5000})
	for _, env := range []event.Envelope{h.deliver(c.succeeded()), h.deliver(c.completed(0))} {
		require.NoError(t, h.handle(env))
		require.NoError(t, h.handle(env))
	}
	assert.Equal(t, 3, h.count("journal_transactions"))
	assert.Equal(t, int64(-95000), h.balance(wallet(c.orders[0].seller)))
}

func TestSameFactUnderNewEventIDIsNoOp(t *testing.T) {
	h := newHarness(t)
	c := newCheckout("stripe", orderSpec{amount: 100000, commission: 5000}, orderSpec{amount: 50000, commission: 2500})
	succ, done, ref := c.succeeded(), c.completed(0), c.refunded(1)
	for range 2 {
		h.deliver(succ)
		h.deliver(done)
		h.deliver(ref)
	}
	assert.Equal(t, 4, h.count("journal_transactions"))
	assert.Equal(t, 6, h.count("processed_events"))
	assert.Equal(t, int64(-95000), h.balance(wallet(c.orders[0].seller)))
	assert.Zero(t, h.total())
}

func TestOrderCompletedBeforePaymentIsReleasedOnCapture(t *testing.T) {
	h := newHarness(t)
	c := newCheckout("fake", orderSpec{amount: 100000, commission: 5000})
	h.deliver(c.completed(0))
	assert.Zero(t, h.count("journal_transactions"))
	assert.Equal(t, 1, h.count("pending_postings"))

	h.deliver(c.succeeded())
	assert.Zero(t, h.count("pending_postings"))
	assert.Equal(t, int64(-95000), h.balance(wallet(c.orders[0].seller)))
	assert.Zero(t, h.balance(escrow(c.orders[0].id)))
	assert.Equal(t, 3, h.count("journal_transactions"))
}

func TestCODOrderCompletedPostsNothing(t *testing.T) {
	h := newHarness(t)
	c := newCheckout("fake", orderSpec{amount: 100000, commission: 0})
	cod := c.completed(0)
	cod.PaymentMethod = event.PaymentMethodCOD
	legacy := c.completed(0)
	legacy.PaymentMethod = ""
	h.deliver(cod)
	h.deliver(legacy)
	assert.Zero(t, h.count("journal_transactions"))
	assert.Zero(t, h.count("pending_postings"))
	assert.Equal(t, 2, h.count("processed_events"))
}

func TestRefundBeforeCaptureIsRetryable(t *testing.T) {
	h := newHarness(t)
	c := newCheckout("fake", orderSpec{amount: 100000, commission: 5000})
	env, err := event.New(c.refunded(0), h.clock.Now())
	require.NoError(t, err)
	err = h.handle(env)
	require.ErrorIs(t, err, app.ErrAllocationMissing)
	assert.Zero(t, h.count("processed_events"), "not marked, so the redelivery is applied")

	h.deliver(c.succeeded())
	require.NoError(t, h.handle(env))
	assert.Zero(t, h.balance(escrow(c.orders[0].id)))
	assert.Equal(t, 3, h.count("journal_transactions"))
}

func TestRefundAfterReleaseIsRejectedAndSkipped(t *testing.T) {
	h := newHarness(t)
	c := newCheckout("fake", orderSpec{amount: 100000, commission: 5000})
	h.deliver(c.succeeded())
	h.deliver(c.completed(0))
	before := h.count("journal_transactions")

	h.deliver(c.refunded(0)) // must return nil: never block the partition
	assert.Equal(t, before, h.count("journal_transactions"))
	assert.Equal(t, int64(-95000), h.balance(wallet(c.orders[0].seller)))
	assert.True(t, h.logged(`"alert":true`))
}

func TestDuplicatePaymentIsRefundedFromSuspense(t *testing.T) {
	h := newHarness(t)
	c := newCheckout("vnpay", orderSpec{amount: 100000, commission: 5000})
	h.deliver(c.succeeded())
	dup := c.succeeded()
	dup.PaymentID = newID().String()
	dup.Allocations = nil
	h.deliver(dup)
	suspense := "checkout_suspense:" + c.id.String()
	assert.Equal(t, int64(-100000), h.balance(suspense))

	h.deliver(event.PaymentRefunded{RefundID: newID().String(), PaymentID: dup.PaymentID, CheckoutID: c.id.String(),
		OrderID: nil, Amount: 100000, Provider: "vnpay"})
	assert.Zero(t, h.balance(suspense))
	assert.Equal(t, int64(100000), h.balance("psp_clearing:vnpay"))
	assert.Zero(t, h.total())
}

func TestSecondPaymentForCapturedOrdersDoesNotDoubleCapture(t *testing.T) {
	h := newHarness(t)
	c := newCheckout("fake", orderSpec{amount: 100000, commission: 5000})
	h.deliver(c.succeeded())
	second := c.succeeded()
	second.PaymentID = newID().String()
	h.deliver(second)

	assert.Equal(t, int64(-100000), h.balance(escrow(c.orders[0].id)), "escrow credited once")
	assert.Equal(t, int64(-100000), h.balance("checkout_suspense:"+c.id.String()), "second charge waits in suspense")
	assert.True(t, h.logged("already captured"))
	assert.Zero(t, h.total())
}

func TestMalformedEventsAreSkipped(t *testing.T) {
	h := newHarness(t)
	c := newCheckout("fake", orderSpec{amount: 100000, commission: 5000})
	mutate := map[string]func(*event.PaymentSucceeded){
		"bad payment id":    func(p *event.PaymentSucceeded) { p.PaymentID = "nope" },
		"bad order id":      func(p *event.PaymentSucceeded) { p.Allocations[0].OrderID = "nope" },
		"zero amount":       func(p *event.PaymentSucceeded) { p.Amount = 0 },
		"unknown provider":  func(p *event.PaymentSucceeded) { p.Provider = "paypal" },
		"alloc > amount":    func(p *event.PaymentSucceeded) { p.Amount = 99999 },
		"commission=amount": func(p *event.PaymentSucceeded) { p.Allocations[0].CommissionAmount = 100000 },
	}
	for name, m := range mutate {
		p := c.succeeded()
		p.PaymentID = newID().String()
		m(&p)
		env, err := event.New(p, h.clock.Now())
		require.NoError(t, err)
		assert.NoError(t, h.handle(env), name)
	}
	bad := event.Envelope{EventID: "not-a-uuid", EventType: event.TypePaymentSucceeded, Version: 1,
		OccurredAt: h.clock.Now(), Data: []byte(`{"payment_id": 1}`)}
	assert.NoError(t, h.handle(bad))
	assert.Zero(t, h.count("journal_transactions"))
	assert.Zero(t, h.count("processed_events"))
}

func TestCompletedAmountsDifferingFromAllocationUseAllocation(t *testing.T) {
	h := newHarness(t)
	c := newCheckout("fake", orderSpec{amount: 100000, commission: 5000})
	h.deliver(c.succeeded())
	done := c.completed(0)
	done.Total, done.CommissionAmount = 999999, 1
	h.deliver(done)
	assert.Equal(t, int64(-95000), h.balance(wallet(c.orders[0].seller)))
	assert.Equal(t, int64(-5000), h.balance("platform_commission_revenue"))
	assert.True(t, h.logged("differ from the captured allocation"))
}

func TestUnrelatedEventTypesAreIgnored(t *testing.T) {
	h := newHarness(t)
	h.deliver(event.PaymentFailed{PaymentID: newID().String(), CheckoutID: newID().String(), Provider: "fake", Reason: "declined"})
	h.deliver(event.OrderCancelled{OrderID: newID().String(), Reason: "x", CancelledBy: event.CancelledByBuyer})
	assert.Zero(t, h.count("processed_events"))
	assert.Zero(t, h.count("journal_transactions"))
}

func TestRefundOfAnotherPaymentIsRejected(t *testing.T) {
	h := newHarness(t)
	c := newCheckout("fake", orderSpec{amount: 100000, commission: 5000})
	h.deliver(c.succeeded())
	before := h.count("journal_transactions")
	ref := c.refunded(0)
	ref.PaymentID = newID().String()
	h.deliver(ref)

	assert.Equal(t, int64(-100000), h.balance(escrow(c.orders[0].id)))
	assert.Equal(t, before, h.count("journal_transactions"))
	assert.True(t, h.logged("refund for another payment"))
}
