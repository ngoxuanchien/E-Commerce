package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/e2e/client"
)

// TestFailedPaymentCanBeRetried verifies that a failed VNPAY payment can be retried
// and completed successfully, with the order transitioning through the correct states.
func TestFailedPaymentCanBeRetried(t *testing.T) {
	w := getWorld(t)

	// Step 1: Create product and baseline wallet
	productA := newProduct(t, w.sellerA, w.categoryID, "Product Retry", 10)
	skuA := productA.Skus[0]

	baseA := walletOf(t, w.sellerA)

	// Step 2: Checkout with VNPAY, 1 line
	lines := map[string]int{skuA.ID: 1}
	checkoutRes := checkoutOnline(t, w.payer, "VNPAY", lines)
	require.Len(t, checkoutRes.Orders, 1)

	orderA := orderFor(t, checkoutRes, w.shopA.ID)
	checkoutID := checkoutRes.CheckoutID

	// Step 3: First payment attempt — fail
	p1 := payFake(t, w.payer, checkoutID, "fail", nil)
	p1 = waitPayment(t, w.payer, p1.ID, "FAILED", 5*time.Second)
	require.NotNil(t, p1.FailureReason, "failed payment should have a failure reason")

	// Step 4: Assert order stays AWAITING_PAYMENT/UNPAID during webhook delay window
	orderA = waitOrder(t, w.payer, orderA.ID, 100*time.Millisecond, // initial fetch
		func(o client.Order) bool {
			return o.Status == "AWAITING_PAYMENT" && o.PaymentStatus == "UNPAID"
		},
		"orderA should be in initial AWAITING_PAYMENT/UNPAID state")

	// Use assert.Never to verify the order doesn't change during the webhook delay window
	neverTimeout := fakeWebhookDelay + 2*time.Second
	assert.Never(t, func() bool {
		o, err := w.payer.Order(ctx(t), orderA.ID)
		if err != nil {
			return false // ignore transient errors
		}
		// Condition: order is NOT still AWAITING_PAYMENT/UNPAID
		return !(o.Status == "AWAITING_PAYMENT" && o.PaymentStatus == "UNPAID")
	}, neverTimeout, 200*time.Millisecond)

	// Step 5: Second payment attempt — succeed (different payment ID)
	p2 := payFake(t, w.payer, checkoutID, "succeed", nil)
	require.NotEqual(t, p1.ID, p2.ID, "retry should create a new payment with different ID")

	p2 = waitPayment(t, w.payer, p2.ID, "SUCCEEDED", 5*time.Second)
	require.Nil(t, p2.FailureReason, "successful payment should have no failure reason")

	// Step 6: Order transitions to PENDING_CONFIRMATION/PAID
	orderA = waitOrder(t, w.payer, orderA.ID, 10*time.Second,
		func(o client.Order) bool {
			return o.Status == "PENDING_CONFIRMATION" && o.PaymentStatus == "PAID"
		},
		"orderA should be PENDING_CONFIRMATION with PAID after successful payment")

	// Step 7: Verify escrow
	waitWallet(t, w.sellerA, 5*time.Second,
		func(wl client.Wallet) bool {
			return wl.InEscrow == baseA.InEscrow+orderA.Total
		},
		"sellerA wallet: InEscrow should increase by orderA.Total")

	// Step 8: Seller cancels the order at PENDING_CONFIRMATION
	_, err := w.sellerA.SellerOrderAction(ctx(t), orderA.ID, "cancel", "Hết hàng")
	require.NoError(t, err)

	// Step 9: Wait for refund to complete within 3*refundInterval + 5s
	refundTimeout := 3*refundInterval + 5*time.Second
	waitOrder(t, w.payer, orderA.ID, refundTimeout,
		func(o client.Order) bool {
			return o.PaymentStatus == "REFUNDED"
		},
		"orderA payment should be REFUNDED after seller cancellation")

	// Step 10: Verify no escrow remains
	waitWallet(t, w.sellerA, 10*time.Second,
		func(wl client.Wallet) bool {
			return wl.InEscrow == baseA.InEscrow
		},
		"sellerA wallet: InEscrow should be released back to original")
}

// TestPaymentTimeoutCancelsAndRestocks verifies that when a payment deadline expires,
// the order is cancelled with the correct reason, stock is restored, and further
// payment attempts are rejected.
func TestPaymentTimeoutCancelsAndRestocks(t *testing.T) {
	w := getWorld(t)

	// Step 1: Create product with stock=5, baseline for stock verification
	productA := newProduct(t, w.sellerA, w.categoryID, "Product Timeout", 5)
	skuA := productA.Skus[0]
	initialStock := 5

	// Step 2: Checkout 2 units with VNPAY, don't pay
	lines := map[string]int{skuA.ID: 2}
	checkoutRes := checkoutOnline(t, w.payer, "VNPAY", lines)
	require.Len(t, checkoutRes.Orders, 1)

	orderA := orderFor(t, checkoutRes, w.shopA.ID)
	checkoutID := checkoutRes.CheckoutID

	// Step 3: Verify stock reserved (5 - 2 = 3 remaining)
	stock := stockOf(t, productA.ID, skuA.ID)
	require.Equal(t, 3, stock, "stock should be reserved: 5 - 2 = 3")

	// Step 4: Wait for order to be cancelled due to payment timeout
	cancelTimeout := paymentTimeout + 3*sweeperInterval + 10*time.Second
	orderA = waitOrder(t, w.payer, orderA.ID, cancelTimeout,
		func(o client.Order) bool {
			return o.Status == "CANCELLED"
		},
		"orderA should be CANCELLED after payment timeout")

	// Step 5: Verify cancel reason is exactly PAYMENT_TIMEOUT
	require.NotNil(t, orderA.CancelReason, "order should have a cancel reason")
	require.Equal(t, "PAYMENT_TIMEOUT", *orderA.CancelReason, "cancel reason must be PAYMENT_TIMEOUT")

	// Step 6: Verify stock is restored back to 5
	restored := false
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		stock = stockOf(t, productA.ID, skuA.ID)
		if stock == initialStock {
			restored = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	require.True(t, restored, "stock should be restored to 5, got %d", stock)

	// Step 7: Attempt to create payment for cancelled order → 409
	_, err := w.payer.CreatePayment(ctx(t), newKey(), checkoutID, nil)
	require.Error(t, err, "CreatePayment on cancelled checkout should fail")
	require.Equal(t, http.StatusConflict, client.StatusOf(err), "should be 409 Conflict")
	require.True(t, client.IsCode(err, "INVALID_STATE_TRANSITION"), "should be INVALID_STATE_TRANSITION")
}
