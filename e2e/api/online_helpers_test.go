package api_test

import (
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/e2e/client"
)

// checkoutOnline clears the cart, sets items by sku→qty, and checks out with
// the specified payment method. Requires all orders to be AWAITING_PAYMENT with
// payment_status UNPAID and non-nil deadline.
func checkoutOnline(t *testing.T, buyer actor, method string, lines map[string]int) client.CheckoutResponse {
	t.Helper()
	require.NoError(t, buyer.ClearCart(ctx(t)))
	for skuID, qty := range lines {
		_, err := buyer.SetCartItem(ctx(t), skuID, qty)
		require.NoError(t, err)
	}
	res, err := buyer.Checkout(ctx(t), newKey(), client.CheckoutRequest{
		ShippingAddress: client.DemoAddress(buyer.User.FullName),
		PaymentMethod:   method,
	}, nil)
	require.NoError(t, err)
	for _, o := range res.Orders {
		require.Equal(t, "AWAITING_PAYMENT", o.Status, "order %s status", o.ID)
		require.Equal(t, "UNPAID", o.PaymentStatus, "order %s payment_status", o.ID)
		require.NotNil(t, o.PaymentDeadline, "order %s payment_deadline", o.ID)
	}
	return res
}

// startPayment creates a payment for the checkout using an idempotency key.
// Requires PENDING status and non-nil RedirectURL.
func startPayment(t *testing.T, buyer actor, checkoutID string) client.Payment {
	t.Helper()
	p, err := buyer.CreatePayment(ctx(t), newKey(), checkoutID, nil)
	require.NoError(t, err)
	require.Equal(t, "PENDING", p.Status, "payment %s status", p.ID)
	require.NotNil(t, p.RedirectURL, "payment %s redirect_url", p.ID)
	return p
}

// payFake calls startPayment then submits via FakeSubmit with the given outcome.
// Returns the started payment.
func payFake(t *testing.T, buyer actor, checkoutID, outcome string, hooks url.Values) client.Payment {
	t.Helper()
	p := startPayment(t, buyer, checkoutID)
	_, err := buyer.FakeSubmit(ctx(t), p.ID, outcome, hooks, nil)
	require.NoError(t, err)
	return p
}

// waitOrder polls every 200ms until the order satisfies ok or within times out.
// Fetches with plain error checks inside the loop, keeps the last value,
// asserts on the last value afterwards with a clear message.
func waitOrder(t *testing.T, a actor, orderID string, within time.Duration, ok func(client.Order) bool, msg string) client.Order {
	t.Helper()
	deadline := time.Now().Add(within)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var order client.Order
	var lastErr error
	for {
		o, err := a.Order(ctx(t), orderID)
		if err == nil {
			order = o
			if ok(order) {
				return order
			}
			lastErr = nil
		} else {
			lastErr = err
		}
		<-ticker.C
		if time.Now().After(deadline) {
			var errMsg string
			if lastErr != nil {
				errMsg = fmt.Sprintf(" (last fetch error: %v)", lastErr)
			}
			require.Failf(t, msg, "order %s: %v%s", orderID, order, errMsg)
			return order
		}
	}
}

// waitPayment polls every 200ms until the payment reaches the desired status
// or within times out. Uses the same polling strategy as waitOrder.
func waitPayment(t *testing.T, buyer actor, paymentID, status string, within time.Duration) client.Payment {
	t.Helper()
	deadline := time.Now().Add(within)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var payment client.Payment
	var lastErr error
	for {
		p, err := buyer.Payment(ctx(t), paymentID)
		if err == nil {
			payment = p
			if payment.Status == status {
				return payment
			}
			lastErr = nil
		} else {
			lastErr = err
		}
		<-ticker.C
		if time.Now().After(deadline) {
			var errMsg string
			if lastErr != nil {
				errMsg = fmt.Sprintf(" (last fetch error: %v)", lastErr)
			}
			require.Failf(t, "waitPayment", "payment %s: expected status %s, got %s%s", paymentID, status, payment.Status, errMsg)
			return payment
		}
	}
}

// walletOf retrieves the seller's current wallet balance.
func walletOf(t *testing.T, seller actor) client.Wallet {
	t.Helper()
	w, err := seller.Wallet(ctx(t))
	require.NoError(t, err)
	return w
}

// waitWallet polls every 200ms until the wallet satisfies ok or within times out.
// Uses the same polling strategy as waitOrder.
func waitWallet(t *testing.T, seller actor, within time.Duration, ok func(client.Wallet) bool, msg string) client.Wallet {
	t.Helper()
	deadline := time.Now().Add(within)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var wallet client.Wallet
	var lastErr error
	for {
		w, err := seller.Wallet(ctx(t))
		if err == nil {
			wallet = w
			if ok(wallet) {
				return wallet
			}
			lastErr = nil
		} else {
			lastErr = err
		}
		<-ticker.C
		if time.Now().After(deadline) {
			var errMsg string
			if lastErr != nil {
				errMsg = fmt.Sprintf(" (last fetch error: %v)", lastErr)
			}
			require.Failf(t, msg, "wallet: %+v%s", wallet, errMsg)
			return wallet
		}
	}
}

// deliver confirms, ships, and delivers an order for the seller.
func deliver(t *testing.T, seller actor, orderID string) {
	t.Helper()
	_, err := seller.SellerOrderAction(ctx(t), orderID, "confirm", "")
	require.NoError(t, err)
	_, err = seller.SellerOrderAction(ctx(t), orderID, "ship", "")
	require.NoError(t, err)
	_, err = seller.SellerOrderAction(ctx(t), orderID, "deliver", "")
	require.NoError(t, err)
}

// orderFor returns the order from a checkout response matching the given shop ID.
// Fails the test if no order is found for that shop.
func orderFor(t *testing.T, res client.CheckoutResponse, shopID string) client.Order {
	t.Helper()
	for _, o := range res.Orders {
		if o.Shop.ID == shopID {
			return o
		}
	}
	require.Failf(t, "orderFor", "no order found for shop %s in checkout %s", shopID, res.CheckoutID)
	return client.Order{}
}

// credit calculates the seller's net credit (Total - CommissionAmount).
func credit(o client.Order) int64 {
	return o.Total - o.CommissionAmount
}
