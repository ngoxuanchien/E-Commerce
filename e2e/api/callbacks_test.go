package api_test

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/e2e/client"
)

// TestDuplicateAndStaleWebhooksApplyOnce verifies that duplicate and stale/out-of-order
// FAKE webhooks apply exactly once: the order and wallet each reflect a single credit.
func TestDuplicateAndStaleWebhooksApplyOnce(t *testing.T) {
	w := getWorld(t)
	defer func() { assertTrialBalance(t) }()

	for _, hook := range []string{"duplicate", "stale_after"} {
		t.Run(hook, func(t *testing.T) {
			// Step 1: Create product and capture baseline wallet
			productA := newProduct(t, w.sellerA, w.categoryID, "Webhook Test "+hook, 10)
			skuA := productA.Skus[0]

			baseA := walletOf(t, w.sellerA)

			// Step 2: Checkout VNPAY (shopA, 1 line)
			lines := map[string]int{skuA.ID: 1}
			checkoutRes := checkoutOnline(t, w.payer, "VNPAY", lines)
			require.Len(t, checkoutRes.Orders, 1)

			orderA := orderFor(t, checkoutRes, w.shopA.ID)
			checkoutID := checkoutRes.CheckoutID

			// Step 3: Submit FAKE payment with "succeed" outcome and the webhook hook
			hooks := url.Values{"hook_webhook": {hook}}
			p := payFake(t, w.payer, checkoutID, "succeed", hooks)
			require.Equal(t, "VNPAY", p.Method)

			// Step 4: Wait for payment to reach SUCCEEDED
			p = waitPayment(t, w.payer, p.ID, "SUCCEEDED", 5*time.Second)
			require.Equal(t, "fake", p.Provider)

			// Step 5: Wait for order to reach PENDING_CONFIRMATION with PAID
			orderA = waitOrder(t, w.payer, orderA.ID, 10*time.Second,
				func(o client.Order) bool {
					return o.Status == "PENDING_CONFIRMATION" && o.PaymentStatus == "PAID"
				},
				"orderA should be PENDING_CONFIRMATION with PAID")

			// Step 6: Assert.Never that order or payment change during webhook delay window
			neverTimeout := 4*fakeWebhookDelay + 2*time.Second
			assert.Never(t, func() bool {
				o, err := w.payer.Order(ctx(t), orderA.ID)
				if err != nil {
					return false // ignore transient errors
				}
				p, err := w.payer.Payment(ctx(t), p.ID)
				if err != nil {
					return false // ignore transient errors
				}
				// Condition: order left PENDING_CONFIRMATION/PAID or payment left SUCCEEDED
				return !(o.Status == "PENDING_CONFIRMATION" && o.PaymentStatus == "PAID" &&
					p.Status == "SUCCEEDED")
			}, neverTimeout, 200*time.Millisecond,
				"order/payment should stay in PENDING_CONFIRMATION/PAID/SUCCEEDED")

			// Step 7: Deliver the order
			deliver(t, w.sellerA, orderA.ID)

			// Step 8: Buyer completes the order
			orderA, err := w.payer.CompleteOrder(ctx(t), orderA.ID)
			require.NoError(t, err)

			// Step 9: Wait for wallet to show credit
			waitWallet(t, w.sellerA, 10*time.Second,
				func(wl client.Wallet) bool {
					return wl.Available == baseA.Available+credit(orderA)
				},
				"sellerA wallet: Available should increase by credit(orderA)")

			// Step 10: Re-fetch order and verify history has exactly one PENDING_CONFIRMATION entry
			orderA, err = w.payer.Order(ctx(t), orderA.ID)
			require.NoError(t, err)
			require.NotEmpty(t, orderA.History)
			transitionCount := 0
			for _, entry := range orderA.History {
				if entry.To == "PENDING_CONFIRMATION" {
					transitionCount++
				}
			}
			require.Equal(t, 1, transitionCount,
				"order history should have exactly one PENDING_CONFIRMATION transition")

			// Step 11: Assert.Never that wallet is credited twice
			finalAvailable := baseA.Available + credit(orderA)
			assert.Never(t, func() bool {
				wl, err := w.sellerA.Wallet(ctx(t))
				if err != nil {
					return false // ignore transient errors
				}
				// Condition: wallet has been credited more than once
				return wl.Available > finalAvailable
			}, 2*time.Second, 200*time.Millisecond,
				"wallet should be credited exactly once")
		})
	}
}

// TestDroppedWebhookIsRecovered verifies that a dropped webhook is recovered by
// either the reconciler or the return URL.
func TestDroppedWebhookIsRecovered(t *testing.T) {
	w := getWorld(t)
	defer func() { assertTrialBalance(t) }()

	t.Run("reconciler", func(t *testing.T) {
		// Step 1: Create product and capture baseline wallet
		productA := newProduct(t, w.sellerA, w.categoryID, "Webhook Dropped Reconciler", 10)
		skuA := productA.Skus[0]

		baseA := walletOf(t, w.sellerA)

		// Step 2: Checkout VNPAY (shopA, 1 line)
		lines := map[string]int{skuA.ID: 1}
		checkoutRes := checkoutOnline(t, w.payer, "VNPAY", lines)
		require.Len(t, checkoutRes.Orders, 1)

		orderA := orderFor(t, checkoutRes, w.shopA.ID)
		checkoutID := checkoutRes.CheckoutID

		// Step 3: Submit FAKE payment with "succeed" outcome and drop hook
		// (webhook is dropped, return URL is never called)
		hooks := url.Values{"hook_webhook": {"drop"}}
		p := payFake(t, w.payer, checkoutID, "succeed", hooks)
		require.Equal(t, "VNPAY", p.Method)

		// Step 4: Payment starts but webhook is dropped
		// Wait for payment to reach SUCCEEDED within reconcileMinAge + 3*reconcileInterval + 2s
		reconcileTimeout := reconcileMinAge + 3*reconcileInterval + 2*time.Second
		p = waitPayment(t, w.payer, p.ID, "SUCCEEDED", reconcileTimeout)
		require.Equal(t, "fake", p.Provider)

		// Step 5: Wait for order to reach PENDING_CONFIRMATION with PAID
		orderA = waitOrder(t, w.payer, orderA.ID, 10*time.Second,
			func(o client.Order) bool {
				return o.Status == "PENDING_CONFIRMATION" && o.PaymentStatus == "PAID"
			},
			"orderA should be PENDING_CONFIRMATION with PAID after reconciler recovery")

		// Step 6: Verify escrow
		waitWallet(t, w.sellerA, 5*time.Second,
			func(wl client.Wallet) bool {
				return wl.InEscrow == baseA.InEscrow+orderA.Total
			},
			"sellerA wallet: InEscrow should increase by orderA.Total")

		// Step 7: Seller cancels the order
		_, err := w.sellerA.SellerOrderAction(ctx(t), orderA.ID, "cancel", "Kiểm thử")
		require.NoError(t, err)

		// Step 8: Wait for order to be REFUNDED within 3*refundInterval + 5s
		refundTimeout := 3*refundInterval + 5*time.Second
		orderA = waitOrder(t, w.payer, orderA.ID, refundTimeout,
			func(o client.Order) bool {
				return o.Status == "CANCELLED" && o.PaymentStatus == "REFUNDED"
			},
			"orderA should be CANCELLED with REFUNDED payment")

		// Step 9: Verify escrow is released
		waitWallet(t, w.sellerA, 10*time.Second,
			func(wl client.Wallet) bool {
				return wl.InEscrow == baseA.InEscrow
			},
			"sellerA wallet: InEscrow should be released")
	})

	t.Run("return_url", func(t *testing.T) {
		// Step 1: Create product and capture baseline wallet
		productA := newProduct(t, w.sellerA, w.categoryID, "Webhook Dropped Return URL", 10)
		skuA := productA.Skus[0]

		baseA := walletOf(t, w.sellerA)

		// Step 2: Checkout VNPAY (shopA, 1 line)
		lines := map[string]int{skuA.ID: 1}
		checkoutRes := checkoutOnline(t, w.payer, "VNPAY", lines)
		require.Len(t, checkoutRes.Orders, 1)

		orderA := orderFor(t, checkoutRes, w.shopA.ID)
		checkoutID := checkoutRes.CheckoutID

		// Step 3: Submit FAKE payment with "succeed" outcome and drop hook
		hooks := url.Values{"hook_webhook": {"drop"}}
		paymentStartTime := time.Now()
		p := payFake(t, w.payer, checkoutID, "succeed", hooks)
		require.Equal(t, "VNPAY", p.Method)

		// Step 4: Immediately call FakeReturn (webhook was dropped, return URL recovers)
		// FakeReturn should return location starting with /checkout/result?payment_id=
		location, err := w.payer.FakeReturn(ctx(t), p.ID)
		require.NoError(t, err)
		require.NotEmpty(t, location)
		require.True(t, strings.HasPrefix(location, "/checkout/result?payment_id="), location)

		// Step 5: Wait for payment to reach SUCCEEDED within 1 second
		// (return path Query must win before reconciler is eligible at reconcileMinAge)
		p = waitPayment(t, w.payer, p.ID, "SUCCEEDED", 1*time.Second)
		require.Equal(t, "fake", p.Provider)
		// Verify SUCCEEDED before reconcileMinAge elapsed (before reconciler becomes eligible)
		elapsedSincePaymentStart := time.Since(paymentStartTime)
		require.Less(t, elapsedSincePaymentStart, reconcileMinAge+1*time.Second,
			"payment should SUCCEED via return URL before reconciler is eligible")

		// Step 6: Wait for order to reach PENDING_CONFIRMATION with PAID
		orderA = waitOrder(t, w.payer, orderA.ID, 10*time.Second,
			func(o client.Order) bool {
				return o.Status == "PENDING_CONFIRMATION" && o.PaymentStatus == "PAID"
			},
			"orderA should be PENDING_CONFIRMATION with PAID after return URL recovery")

		// Step 7: Verify escrow
		waitWallet(t, w.sellerA, 5*time.Second,
			func(wl client.Wallet) bool {
				return wl.InEscrow == baseA.InEscrow+orderA.Total
			},
			"sellerA wallet: InEscrow should increase by orderA.Total")

		// Step 8: Seller cancels the order
		_, err = w.sellerA.SellerOrderAction(ctx(t), orderA.ID, "cancel", "Kiểm thử")
		require.NoError(t, err)

		// Step 9: Wait for order to be REFUNDED within 3*refundInterval + 5s
		refundTimeout := 3*refundInterval + 5*time.Second
		orderA = waitOrder(t, w.payer, orderA.ID, refundTimeout,
			func(o client.Order) bool {
				return o.Status == "CANCELLED" && o.PaymentStatus == "REFUNDED"
			},
			"orderA should be CANCELLED with REFUNDED payment")

		// Step 10: Verify escrow is released
		waitWallet(t, w.sellerA, 10*time.Second,
			func(wl client.Wallet) bool {
				return wl.InEscrow == baseA.InEscrow
			},
			"sellerA wallet: InEscrow should be released")
	})
}
