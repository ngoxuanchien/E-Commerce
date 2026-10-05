package api_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/e2e/client"
)

// TestOnlineCheckoutEscrowAndAutoComplete verifies the VNPAY online payment flow:
// checkout → payment → orders in PENDING_CONFIRMATION → delivery → auto-complete after sweeper cycles
func TestOnlineCheckoutEscrowAndAutoComplete(t *testing.T) {
	w := getWorld(t)

	// Step 1: Create products and capture baseline wallets
	productA := newProduct(t, w.sellerA, w.categoryID, "Product A", 10)
	productB := newProduct(t, w.sellerB, w.categoryID, "Product B", 10)
	skuA := productA.Skus[0]
	skuB := productB.Skus[0]

	baseA := walletOf(t, w.sellerA)
	baseB := walletOf(t, w.sellerB)

	// Step 2: Checkout with VNPAY, 2 items (qty=2 from shopA, qty=1 from shopB)
	lines := map[string]int{skuA.ID: 2, skuB.ID: 1}
	checkoutRes := checkoutOnline(t, w.payer, "VNPAY", lines)
	require.Len(t, checkoutRes.Orders, 2, "expected 2 orders from 2 shops")

	orderA := checkoutRes.Orders[0]
	orderB := checkoutRes.Orders[1]
	checkoutID := checkoutRes.CheckoutID

	// Verify both orders have correct commission (500 bps = 5%)
	require.Equal(t, 500, orderA.CommissionRateBps)
	require.Equal(t, 500, orderB.CommissionRateBps)
	expectedCommA := orderA.Subtotal * 500 / 10000
	expectedCommB := orderB.Subtotal * 500 / 10000
	require.Equal(t, expectedCommA, orderA.CommissionAmount)
	require.Equal(t, expectedCommB, orderB.CommissionAmount)

	// Step 3: Create and submit FAKE payment with "succeed" outcome
	p := payFake(t, w.payer, checkoutID, "succeed", nil)
	require.Equal(t, "VNPAY", p.Method)

	// Step 4: Wait for payment to reach SUCCEEDED
	p = waitPayment(t, w.payer, p.ID, "SUCCEEDED", 5*time.Second)
	require.Equal(t, "fake", p.Provider)
	require.Equal(t, "VNPAY", p.Method)
	require.Equal(t, orderA.Total+orderB.Total, p.Amount)

	// Step 5: Both orders reach PENDING_CONFIRMATION with PAID status
	orderA = waitOrder(t, w.payer, orderA.ID, 10*time.Second,
		func(o client.Order) bool {
			return o.Status == "PENDING_CONFIRMATION" && o.PaymentStatus == "PAID"
		},
		"orderA should be PENDING_CONFIRMATION with PAID")
	orderB = waitOrder(t, w.payer, orderB.ID, 10*time.Second,
		func(o client.Order) bool {
			return o.Status == "PENDING_CONFIRMATION" && o.PaymentStatus == "PAID"
		},
		"orderB should be PENDING_CONFIRMATION with PAID")

	// Step 6: Wallet shows escrow for both orders
	waitWallet(t, w.sellerA, 5*time.Second,
		func(wl client.Wallet) bool {
			return wl.InEscrow == baseA.InEscrow+orderA.Total
		},
		"sellerA wallet: InEscrow should increase by orderA.Total")
	waitWallet(t, w.sellerB, 5*time.Second,
		func(wl client.Wallet) bool {
			return wl.InEscrow == baseB.InEscrow+orderB.Total
		},
		"sellerB wallet: InEscrow should increase by orderB.Total")

	// Step 7: Deliver orderA; wait for auto-complete within autoCompleteAfter + 3*sweeperInterval + 5s
	deliver(t, w.sellerA, orderA.ID)
	autoCompleteTimeout := autoCompleteAfter + 3*sweeperInterval + 5*time.Second
	orderA = waitOrder(t, w.payer, orderA.ID, autoCompleteTimeout,
		func(o client.Order) bool {
			return o.Status == "COMPLETED"
		},
		"orderA should auto-complete after delivery")

	// Verify the last history entry is COMPLETED by system
	require.NotEmpty(t, orderA.History)
	lastEntry := orderA.History[len(orderA.History)-1]
	assert.Equal(t, "COMPLETED", lastEntry.To)
	assert.Equal(t, "system", lastEntry.ActorRole)

	// Step 8: Verify sellerA wallet: Available increased, InEscrow released
	waitWallet(t, w.sellerA, 10*time.Second,
		func(wl client.Wallet) bool {
			return wl.Available == baseA.Available+credit(orderA) &&
				wl.InEscrow == baseA.InEscrow
		},
		"sellerA wallet: Available should increase by credit(orderA), InEscrow released")

	// Step 9: Complete orderB (deliver + buyer complete)
	deliver(t, w.sellerB, orderB.ID)
	orderB, err := w.payer.CompleteOrder(ctx(t), orderB.ID)
	require.NoError(t, err)

	// Wait for auto-complete if not already COMPLETED
	if orderB.Status != "COMPLETED" {
		orderB = waitOrder(t, w.payer, orderB.ID, autoCompleteTimeout,
			func(o client.Order) bool {
				return o.Status == "COMPLETED"
			},
			"orderB should auto-complete")
	}

	// Step 10: Verify sellerB wallet: Available increased
	waitWallet(t, w.sellerB, 10*time.Second,
		func(wl client.Wallet) bool {
			return wl.Available == baseB.Available+credit(orderB) &&
				wl.InEscrow == baseB.InEscrow
		},
		"sellerB wallet: Available should increase by credit(orderB), InEscrow released")
}

// TestStripeMethodUsesItsAdapter verifies that STRIPE payment method routes to
// the correct adapter and accepts FAKE provider for testing.
func TestStripeMethodUsesItsAdapter(t *testing.T) {
	w := getWorld(t)

	// Create product on shopA
	productA := newProduct(t, w.sellerA, w.categoryID, "Stripe Test Product", 10)
	skuA := productA.Skus[0]

	// Baseline wallet
	baseA := walletOf(t, w.sellerA)

	// Checkout with STRIPE method
	lines := map[string]int{skuA.ID: 1}
	checkoutRes := checkoutOnline(t, w.payer, "STRIPE", lines)
	require.Len(t, checkoutRes.Orders, 1)

	orderA := checkoutRes.Orders[0]
	checkoutID := checkoutRes.CheckoutID

	// Create and submit FAKE payment with "succeed" outcome
	p := payFake(t, w.payer, checkoutID, "succeed", nil)
	require.Equal(t, "STRIPE", p.Method)

	// Wait for payment to reach SUCCEEDED
	p = waitPayment(t, w.payer, p.ID, "SUCCEEDED", 5*time.Second)
	require.Equal(t, "fake", p.Provider)
	require.Equal(t, "STRIPE", p.Method)
	require.Equal(t, orderA.Total, p.Amount)

	// Order should be PENDING_CONFIRMATION with PAID
	orderA = waitOrder(t, w.payer, orderA.ID, 10*time.Second,
		func(o client.Order) bool {
			return o.Status == "PENDING_CONFIRMATION" && o.PaymentStatus == "PAID"
		},
		"orderA should be PENDING_CONFIRMATION with PAID after STRIPE payment")

	// Deliver order
	deliver(t, w.sellerA, orderA.ID)

	// Complete order as buyer
	_, err := w.payer.CompleteOrder(ctx(t), orderA.ID)
	require.NoError(t, err)

	// Wait for auto-complete or explicit COMPLETED
	autoCompleteTimeout := autoCompleteAfter + 3*sweeperInterval + 5*time.Second
	orderA = waitOrder(t, w.payer, orderA.ID, autoCompleteTimeout,
		func(o client.Order) bool {
			return o.Status == "COMPLETED"
		},
		"orderA should be COMPLETED after buyer complete")

	// Verify sellerA wallet is credited
	waitWallet(t, w.sellerA, 10*time.Second,
		func(wl client.Wallet) bool {
			return wl.Available == baseA.Available+credit(orderA)
		},
		"sellerA wallet: Available should increase by credit(orderA)")
}
