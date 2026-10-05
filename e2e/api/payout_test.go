package api_test

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/e2e/client"
)

// earn is a local helper: payer buys one new shopC product (price 100,000, qty 1)
// with VNPAY, pays with FAKE, seller C delivers, buyer completes, waits for wallet
// credit, and returns credit(order).
func earn(t *testing.T, w *world) int64 {
	t.Helper()

	// Create product on shopC: 1 SKU with 10 stock, price 100,000
	product := newProduct(t, w.sellerC, w.categoryID, "Earn Product", 10)
	sku := product.Skus[0]

	// Checkout with VNPAY, 1 item
	lines := map[string]int{sku.ID: 1}
	checkoutRes := checkoutOnline(t, w.payer, "VNPAY", lines)
	require.Len(t, checkoutRes.Orders, 1)

	order := orderFor(t, checkoutRes, w.shopC.ID)
	checkoutID := checkoutRes.CheckoutID

	// Create and submit FAKE payment with "succeed" outcome
	p := payFake(t, w.payer, checkoutID, "succeed", nil)
	require.Equal(t, "VNPAY", p.Method)

	// Wait for payment to reach SUCCEEDED
	p = waitPayment(t, w.payer, p.ID, "SUCCEEDED", 5*time.Second)
	require.Equal(t, "fake", p.Provider)

	// Wait for order to reach PENDING_CONFIRMATION with PAID
	order = waitOrder(t, w.payer, order.ID, 10*time.Second,
		func(o client.Order) bool {
			return o.Status == "PENDING_CONFIRMATION" && o.PaymentStatus == "PAID"
		},
		"order should be PENDING_CONFIRMATION with PAID")

	// Seller C delivers
	deliver(t, w.sellerC, order.ID)

	// Buyer completes order
	order, err := w.payer.CompleteOrder(ctx(t), order.ID)
	require.NoError(t, err)

	// Wait for wallet to be credited
	waitWallet(t, w.sellerC, 10*time.Second,
		func(wl client.Wallet) bool {
			return wl.Available > 0
		},
		"sellerC wallet should be credited")

	return credit(order)
}

// TestPayoutSettlesAndCannotOverdraw verifies the payout settlement flow:
// 1. Seller earns credit and requests a payout for the full amount → REQUESTED
// 2. Payout settles automatically → SETTLED
// 3. Attempting to payout below minimum or above balance fails
// 4. Concurrent payout requests at the same amount succeed for one and fail for one
func TestPayoutSettlesAndCannotOverdraw(t *testing.T) {
	w := getWorld(t)

	// Step 1: Earn and capture the available balance
	amount1 := earn(t, w)
	wallet1 := walletOf(t, w.sellerC)
	require.Greater(t, wallet1.Available, int64(0), "wallet should have available balance after earn")
	require.Equal(t, wallet1.Available, amount1, "available balance should equal earned credit")

	// Step 2: Request payout for full available amount
	payoutRes, err := w.sellerC.CreatePayout(ctx(t), newKey(), wallet1.Available)
	require.NoError(t, err, "CreatePayout should succeed")
	require.Equal(t, "REQUESTED", payoutRes.Status, "payout should be REQUESTED")
	payoutID1 := payoutRes.ID

	// Verify wallet reflects pending payout
	wallet := walletOf(t, w.sellerC)
	require.Equal(t, int64(0), wallet.Available, "Available should be 0 after payout request")
	require.Equal(t, wallet1.Available, wallet.PendingPayout, "PendingPayout should equal requested amount")

	// Step 3: Poll Payouts until payout is SETTLED
	settleTimeout := payoutSettleDelay + 3*payoutSettleInterval + 5*time.Second
	deadline := time.Now().Add(settleTimeout)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	var settledPayout client.Payout
	for {
		payouts, err := w.sellerC.Payouts(ctx(t), "")
		require.NoError(t, err, "Payouts fetch should succeed")
		require.NotEmpty(t, payouts.Items, "payouts list should not be empty")

		// Find our payout
		for _, p := range payouts.Items {
			if p.ID == payoutID1 {
				settledPayout = p
				if p.Status == "SETTLED" {
					goto settled
				}
				break
			}
		}

		select {
		case <-ticker.C:
			if time.Now().After(deadline) {
				t.Fatalf("payout %s did not settle within %v, last status: %s", payoutID1, settleTimeout, settledPayout.Status)
			}
		}
	}

settled:
	require.Equal(t, "SETTLED", settledPayout.Status, "payout should be SETTLED")

	// Verify wallet after settlement
	wallet = walletOf(t, w.sellerC)
	require.Equal(t, int64(0), wallet.Available, "Available should remain 0")
	require.Equal(t, int64(0), wallet.PendingPayout, "PendingPayout should be 0 after settlement")

	// Verify WalletEntries contains PAYOUT_REQUEST entry
	entries, err := w.sellerC.WalletEntries(ctx(t))
	require.NoError(t, err)
	var foundPayoutEntry bool
	for _, entry := range entries.Items {
		if entry.Kind == "PAYOUT_REQUEST" && entry.PayoutID != nil && *entry.PayoutID == payoutID1 {
			foundPayoutEntry = true
			require.Equal(t, -wallet1.Available, entry.Amount, "PAYOUT_REQUEST amount should be negative of requested amount")
			break
		}
	}
	require.True(t, foundPayoutEntry, "PAYOUT_REQUEST entry should exist in wallet entries")

	// Step 4: Verify validation errors
	// Payout of 1 (< minimum 50,000) → VALIDATION_ERROR
	_, err = w.sellerC.CreatePayout(ctx(t), newKey(), 1)
	require.Error(t, err)
	require.True(t, client.IsCode(err, "VALIDATION_ERROR"), "CreatePayout(1) should be VALIDATION_ERROR, got: %v", err)
	require.Equal(t, 400, client.StatusOf(err), "VALIDATION_ERROR should be 400")

	// Payout of 50,000 (available is 0) → INSUFFICIENT_FUNDS
	_, err = w.sellerC.CreatePayout(ctx(t), newKey(), 50_000)
	require.Error(t, err)
	require.True(t, client.IsCode(err, "INSUFFICIENT_FUNDS"), "CreatePayout(50000) should be INSUFFICIENT_FUNDS, got: %v", err)
	require.Equal(t, 409, client.StatusOf(err), "INSUFFICIENT_FUNDS should be 409")

	// Step 5: Earn again and test concurrent payouts
	amount2 := earn(t, w)
	wallet2 := walletOf(t, w.sellerC)
	require.Equal(t, amount2, wallet2.Available, "wallet should have new earned credit")

	// Two concurrent CreatePayout calls with 70,000 each
	var wg sync.WaitGroup
	var payout2Res client.Payout
	var payout2Err error
	var payout3Res client.Payout
	var payout3Err error

	wg.Add(1)
	go func() {
		defer wg.Done()
		payout2Res, payout2Err = w.sellerC.CreatePayout(ctx(t), newKey(), 70_000)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		payout3Res, payout3Err = w.sellerC.CreatePayout(ctx(t), newKey(), 70_000)
	}()

	wg.Wait()

	// Exactly one should succeed (201 REQUESTED), one should fail (409 INSUFFICIENT_FUNDS)
	successCount := 0
	failCount := 0
	var successPayout client.Payout

	if payout2Err != nil {
		failCount++
		require.True(t, client.IsCode(payout2Err, "INSUFFICIENT_FUNDS"), "payout2 error should be INSUFFICIENT_FUNDS, got: %v", payout2Err)
	} else {
		successCount++
		require.Equal(t, "REQUESTED", payout2Res.Status, "successful payout should be REQUESTED")
		successPayout = payout2Res
	}

	if payout3Err != nil {
		failCount++
		require.True(t, client.IsCode(payout3Err, "INSUFFICIENT_FUNDS"), "payout3 error should be INSUFFICIENT_FUNDS, got: %v", payout3Err)
	} else {
		successCount++
		require.Equal(t, "REQUESTED", payout3Res.Status, "successful payout should be REQUESTED")
		successPayout = payout3Res
	}

	require.Equal(t, 1, successCount, "exactly one concurrent payout should succeed")
	require.Equal(t, 1, failCount, "exactly one concurrent payout should fail")

	// Wait for the successful payout to settle
	deadline = time.Now().Add(settleTimeout)
	ticker2 := time.NewTicker(200 * time.Millisecond)
	defer ticker2.Stop()

	var winnerSettled client.Payout
	for {
		payouts, err := w.sellerC.Payouts(ctx(t), "")
		require.NoError(t, err)

		for _, p := range payouts.Items {
			if p.ID == successPayout.ID {
				winnerSettled = p
				if p.Status == "SETTLED" {
					goto winnerSettled
				}
				break
			}
		}

		select {
		case <-ticker2.C:
			if time.Now().After(deadline) {
				t.Fatalf("winner payout %s did not settle within %v", successPayout.ID, settleTimeout)
			}
		}
	}

winnerSettled:
	require.Equal(t, "SETTLED", winnerSettled.Status, "winner payout should be SETTLED")
}
