package api_test

import (
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/e2e/client"
)

// TestDoubleChargeIsRefunded verifies that when a second payment succeeds while
// a first payment is pending (but uncancellable), the second becomes DUPLICATE
// and is refunded automatically. The order is paid once, and escrow is held only once.
func TestDoubleChargeIsRefunded(t *testing.T) {
	w := getWorld(t)

	// Step 1: Create product and capture baseline wallet
	productA := newProduct(t, w.sellerA, w.categoryID, "Double Charge Test", 10)
	skuA := productA.Skus[0]

	baseA := walletOf(t, w.sellerA)

	// Step 2: Checkout VNPAY (shopA, 1 line)
	lines := map[string]int{skuA.ID: 1}
	checkoutRes := checkoutOnline(t, w.payer, "VNPAY", lines)
	require.Len(t, checkoutRes.Orders, 1)

	orderA := orderFor(t, checkoutRes, w.shopA.ID)
	checkoutID := checkoutRes.CheckoutID

	// Step 3: Start payment p1 and arm it with hook_cancel=unsupported
	p1 := startPayment(t, w.payer, checkoutID)
	require.Equal(t, "VNPAY", p1.Method)

	err := w.payer.FakeArm(ctx(t), p1.ID, url.Values{"hook_cancel": {"unsupported"}})
	require.NoError(t, err, "FakeArm should succeed")

	// Step 4: Create p2 while p1 is open but uncancellable (should succeed)
	p2 := startPayment(t, w.payer, checkoutID)
	require.Equal(t, "VNPAY", p2.Method)
	require.NotEqual(t, p1.ID, p2.ID, "p1 and p2 should be different payments")

	// Step 5: Submit p2 with "succeed" outcome → p2 becomes SUCCEEDED
	_, err = w.payer.FakeSubmit(ctx(t), p2.ID, "succeed", nil, nil)
	require.NoError(t, err)

	// Step 6: Wait for p2 to reach SUCCEEDED
	p2 = waitPayment(t, w.payer, p2.ID, "SUCCEEDED", 5*time.Second)
	require.Equal(t, "fake", p2.Provider)

	// Step 7: Wait for order to reach PENDING_CONFIRMATION with PAID
	orderA = waitOrder(t, w.payer, orderA.ID, 10*time.Second,
		func(o client.Order) bool {
			return o.Status == "PENDING_CONFIRMATION" && o.PaymentStatus == "PAID"
		},
		"orderA should be PENDING_CONFIRMATION with PAID after p2 succeeds")

	// Step 8: Verify escrow increased by exactly orderA.Total (not twice)
	waitWallet(t, w.sellerA, 5*time.Second,
		func(wl client.Wallet) bool {
			return wl.InEscrow == baseA.InEscrow+orderA.Total
		},
		"sellerA wallet: InEscrow should increase by orderA.Total exactly once")

	// Step 9: Submit p1 with "succeed" outcome → p1 should become DUPLICATE
	_, err = w.payer.FakeSubmit(ctx(t), p1.ID, "succeed", nil, nil)
	require.NoError(t, err)

	// Step 10: Wait for p1 to reach DUPLICATE within 10s
	p1 = waitPayment(t, w.payer, p1.ID, "DUPLICATE", 10*time.Second)
	require.Equal(t, "fake", p1.Provider)

	// Step 11: Verify p2 stays SUCCEEDED
	p2Fetched, err := w.payer.Payment(ctx(t), p2.ID)
	require.NoError(t, err)
	require.Equal(t, "SUCCEEDED", p2Fetched.Status, "p2 should stay SUCCEEDED")

	// Step 12: Verify order is still PAID with one PENDING_CONFIRMATION history entry
	orderA, err = w.payer.Order(ctx(t), orderA.ID)
	require.NoError(t, err)
	require.Equal(t, "PENDING_CONFIRMATION", orderA.Status)
	require.Equal(t, "PAID", orderA.PaymentStatus)

	// Count PENDING_CONFIRMATION transitions in history
	transitionCount := 0
	for _, entry := range orderA.History {
		if entry.To == "PENDING_CONFIRMATION" {
			transitionCount++
		}
	}
	require.Equal(t, 1, transitionCount, "order history should have exactly one PENDING_CONFIRMATION transition")

	// Step 13: Assert.Never that escrow grows beyond baseA.InEscrow + orderA.Total in the next 2 seconds
	finalEscrow := baseA.InEscrow + orderA.Total
	assert.Never(t, func() bool {
		wl, err := w.sellerA.Wallet(ctx(t))
		if err != nil {
			return false // ignore transient errors
		}
		return wl.InEscrow > finalEscrow
	}, 2*time.Second, 200*time.Millisecond,
		"escrow should not increase beyond one order value")

	// Step 14: Finish the order (deliver + complete + credit)
	deliver(t, w.sellerA, orderA.ID)

	orderA, err = w.payer.CompleteOrder(ctx(t), orderA.ID)
	require.NoError(t, err)

	// Step 15: Verify sellerA wallet: Available increased, InEscrow released
	waitWallet(t, w.sellerA, 10*time.Second,
		func(wl client.Wallet) bool {
			return wl.Available == baseA.Available+credit(orderA) &&
				wl.InEscrow == baseA.InEscrow
		},
		"sellerA wallet: Available should increase by credit(orderA), InEscrow released")
}

// TestLateSuccessAfterDeadlineRefundsBothOrders verifies that a payment succeeding
// after its deadline has already cancelled the orders is refunded for both orders.
// The orders are cancelled by the sweeper when the payment expires before being paid.
func TestLateSuccessAfterDeadlineRefundsBothOrders(t *testing.T) {
	w := getWorld(t)

	// Step 1: Create products and capture baseline wallets
	productA := newProduct(t, w.sellerA, w.categoryID, "Late Success Test A", 10)
	productB := newProduct(t, w.sellerB, w.categoryID, "Late Success Test B", 10)
	skuA := productA.Skus[0]
	skuB := productB.Skus[0]

	baseA := walletOf(t, w.sellerA)
	baseB := walletOf(t, w.sellerB)

	// Step 2: Checkout VNPAY across shopA + shopB (qty=1 from each)
	lines := map[string]int{skuA.ID: 1, skuB.ID: 1}
	checkoutRes := checkoutOnline(t, w.payer, "VNPAY", lines)
	require.Len(t, checkoutRes.Orders, 2, "expected 2 orders from 2 shops")

	orderA := orderFor(t, checkoutRes, w.shopA.ID)
	orderB := orderFor(t, checkoutRes, w.shopB.ID)
	checkoutID := checkoutRes.CheckoutID

	// Step 3: Start payment and arm it with hook_cancel=unsupported
	// (payment-svc reports still-payable attempt as UNAVAILABLE to sweeper,
	// orders are cancelled only after attempt expires)
	p := startPayment(t, w.payer, checkoutID)
	require.Equal(t, "VNPAY", p.Method)

	err := w.payer.FakeArm(ctx(t), p.ID, url.Values{"hook_cancel": {"unsupported"}})
	require.NoError(t, err, "FakeArm should succeed")

	// Step 4: Wait until both orders are CANCELLED
	// Deadline calculation: time.Until(p.ExpiresAt) + paymentTimeout + 3*sweeperInterval + 10s
	expiresAt, err := time.Parse(time.RFC3339Nano, p.ExpiresAt)
	require.NoError(t, err, "parse p.ExpiresAt")

	timeUntilExpiry := time.Until(expiresAt)
	cancelTimeout := timeUntilExpiry + paymentTimeout + 3*sweeperInterval + 10*time.Second

	orderA = waitOrder(t, w.payer, orderA.ID, cancelTimeout,
		func(o client.Order) bool {
			return o.Status == "CANCELLED"
		},
		"orderA should be CANCELLED after payment expires")
	orderB = waitOrder(t, w.payer, orderB.ID, cancelTimeout,
		func(o client.Order) bool {
			return o.Status == "CANCELLED"
		},
		"orderB should be CANCELLED after payment expires")

	// Step 5: Submit payment with "succeed" outcome after deadline has passed
	_, err = w.payer.FakeSubmit(ctx(t), p.ID, "succeed", nil, nil)
	require.NoError(t, err)

	// Step 6: Wait for payment to reach SUCCEEDED
	p = waitPayment(t, w.payer, p.ID, "SUCCEEDED", 5*time.Second)
	require.Equal(t, "fake", p.Provider)

	// Step 7: Both orders should be refunded within 3*refundInterval + 5s
	refundTimeout := 3*refundInterval + 5*time.Second

	orderA = waitOrder(t, w.payer, orderA.ID, refundTimeout,
		func(o client.Order) bool {
			return o.Status == "CANCELLED" && o.PaymentStatus == "REFUNDED"
		},
		"orderA should be CANCELLED with REFUNDED payment status after late success")
	orderB = waitOrder(t, w.payer, orderB.ID, refundTimeout,
		func(o client.Order) bool {
			return o.Status == "CANCELLED" && o.PaymentStatus == "REFUNDED"
		},
		"orderB should be CANCELLED with REFUNDED payment status after late success")

	// Step 8: Verify both sellers' InEscrow back to baseline
	waitWallet(t, w.sellerA, 10*time.Second,
		func(wl client.Wallet) bool {
			return wl.InEscrow == baseA.InEscrow
		},
		"sellerA wallet: InEscrow should be released back to baseline")
	waitWallet(t, w.sellerB, 10*time.Second,
		func(wl client.Wallet) bool {
			return wl.InEscrow == baseB.InEscrow
		},
		"sellerB wallet: InEscrow should be released back to baseline")
}
