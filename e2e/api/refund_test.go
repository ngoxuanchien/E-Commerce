package api_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/e2e/client"
)

// TestSellerCancelAfterPaymentRefundsThatOrder verifies the refund flow:
// checkout VNPAY across shopA + shopB, pay succeed → both PAID; seller A cancels orderA
// → orderA REFUNDED, ledger escrow reversed, orderB unaffected.
func TestSellerCancelAfterPaymentRefundsThatOrder(t *testing.T) {
	w := getWorld(t)

	// Step 1: Create products and capture baseline wallets
	productA := newProduct(t, w.sellerA, w.categoryID, "Product A Refund", 10)
	productB := newProduct(t, w.sellerB, w.categoryID, "Product B Refund", 10)
	skuA := productA.Skus[0]
	skuB := productB.Skus[0]

	baseA := walletOf(t, w.sellerA)
	baseB := walletOf(t, w.sellerB)

	// Step 2: Checkout with VNPAY, 2 items (qty=2 from shopA, qty=1 from shopB)
	lines := map[string]int{skuA.ID: 2, skuB.ID: 1}
	checkoutRes := checkoutOnline(t, w.payer, "VNPAY", lines)
	require.Len(t, checkoutRes.Orders, 2, "expected 2 orders from 2 shops")

	orderA := orderFor(t, checkoutRes, w.shopA.ID)
	orderB := orderFor(t, checkoutRes, w.shopB.ID)
	checkoutID := checkoutRes.CheckoutID

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

	// Step 6: Wait for wallets to show escrow for both orders
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

	// Step 7: Seller A confirms, then cancels orderA (reason "Hết hàng")
	o, err := w.sellerA.SellerOrderAction(ctx(t), orderA.ID, "confirm", "")
	require.NoError(t, err)
	require.Equal(t, "CONFIRMED", o.Status)
	o, err = w.sellerA.SellerOrderAction(ctx(t), orderA.ID, "cancel", "Hết hàng")
	require.NoError(t, err)
	require.Equal(t, "CANCELLED", o.Status)

	// Step 8: Wait for orderA to be CANCELLED with PaymentStatus REFUNDED within 3*refundInterval + 5s
	refundTimeout := 3*refundInterval + 5*time.Second
	orderA = waitOrder(t, w.payer, orderA.ID, refundTimeout,
		func(o client.Order) bool {
			return o.Status == "CANCELLED" && o.PaymentStatus == "REFUNDED"
		},
		"orderA should be CANCELLED with REFUNDED payment after seller cancellation")

	// Step 9: Verify sellerA wallet: InEscrow released, Available unchanged
	waitWallet(t, w.sellerA, 10*time.Second,
		func(wl client.Wallet) bool {
			return wl.InEscrow == baseA.InEscrow && wl.Available == baseA.Available
		},
		"sellerA wallet: InEscrow released, Available unchanged")

	// Step 10: OrderB still PENDING_CONFIRMATION/PAID, sellerB's escrow held
	orderB = waitOrder(t, w.payer, orderB.ID, 2*time.Second,
		func(o client.Order) bool {
			return o.Status == "PENDING_CONFIRMATION" && o.PaymentStatus == "PAID"
		},
		"orderB should still be PENDING_CONFIRMATION with PAID")
	waitWallet(t, w.sellerB, 2*time.Second,
		func(wl client.Wallet) bool {
			return wl.InEscrow == baseB.InEscrow+orderB.Total
		},
		"sellerB wallet: InEscrow still held")

	// Step 11: Finish orderB (deliver + buyer complete)
	deliver(t, w.sellerB, orderB.ID)
	orderB, err = w.payer.CompleteOrder(ctx(t), orderB.ID)
	require.NoError(t, err)

	// Step 12: Verify sellerB wallet: Available increased, InEscrow released
	waitWallet(t, w.sellerB, 10*time.Second,
		func(wl client.Wallet) bool {
			return wl.Available == baseB.Available+credit(orderB) &&
				wl.InEscrow == baseB.InEscrow
		},
		"sellerB wallet: Available should increase by credit(orderB), InEscrow released")
}

// TestCODCheckoutNeverTouchesTheLedger verifies that COD (P1) orders never interact
// with the ledger: no escrow, no commission, payment_status stays NONE.
func TestCODCheckoutNeverTouchesTheLedger(t *testing.T) {
	w := getWorld(t)

	// Step 1: Create product on shopB
	productB := newProduct(t, w.sellerB, w.categoryID, "COD Product", 10)
	skuB := productB.Skus[0]

	// Step 2: Take snapshot of sellerB's wallet
	baseB := walletOf(t, w.sellerB)

	// Step 3: COD checkout on shopB (P1 path: SetCartItem + Checkout with PaymentMethod: "COD")
	buyer := w.payer
	require.NoError(t, buyer.ClearCart(ctx(t)))
	_, err := buyer.SetCartItem(ctx(t), skuB.ID, 1)
	require.NoError(t, err)
	checkoutRes, err := buyer.Checkout(ctx(t), newKey(), client.CheckoutRequest{
		SkuIDs:          []string{skuB.ID},
		ShippingAddress: client.DemoAddress("Người mua COD"),
		PaymentMethod:   "COD",
	}, nil)
	require.NoError(t, err)
	require.Len(t, checkoutRes.Orders, 1)

	orderB := checkoutRes.Orders[0]

	// Step 4: Verify COD order properties: no escrow, no commission, no deadline
	require.Equal(t, "COD", orderB.PaymentMethod, "orderB payment method should be COD")
	require.Equal(t, "NONE", orderB.PaymentStatus, "orderB payment_status should be NONE")
	require.Nil(t, orderB.PaymentDeadline, "orderB payment_deadline should be nil")
	require.Equal(t, int64(0), orderB.CommissionAmount, "orderB commission_amount should be 0")
	require.Equal(t, "PENDING_CONFIRMATION", orderB.Status, "orderB status should be PENDING_CONFIRMATION")

	// Step 5: Deliver and complete the order
	deliver(t, w.sellerB, orderB.ID)
	orderB, err = buyer.CompleteOrder(ctx(t), orderB.ID)
	require.NoError(t, err)
	require.Equal(t, "COMPLETED", orderB.Status, "orderB should be COMPLETED")

	// Step 6: Assert.Never that sellerB's wallet changes for 2*sweeperInterval + 2s
	assert.Never(t, func() bool {
		currentB, err := w.sellerB.Wallet(ctx(t))
		if err != nil {
			return true // Fail loudly on endpoint error
		}
		// Wallet changes if Available or InEscrow differ from baseline
		return currentB.Available != baseB.Available || currentB.InEscrow != baseB.InEscrow
	}, 2*sweeperInterval+2*time.Second, 200*time.Millisecond,
		"COD order should not affect ledger: wallet should not change")
}
