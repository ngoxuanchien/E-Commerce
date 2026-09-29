package api_test

import (
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/e2e/client"
)

func ordersOfCheckout(t *testing.T, b actor, checkoutID string) int {
	t.Helper()
	page, err := b.Orders(ctx(t), "")
	require.NoError(t, err)
	n := 0
	for _, o := range page.Items {
		if o.CheckoutID == checkoutID {
			n++
		}
	}
	return n
}

func TestCheckoutReplaysSameKeyAndRejectsReusedKey(t *testing.T) {
	w := getWorld(t)
	buyer := w.buyers[2]
	require.NoError(t, buyer.ClearCart(ctx(t)))
	pa := newProduct(t, w.sellerA, w.categoryID, "Khay tre đan "+w.run, 10)
	pb := newProduct(t, w.sellerB, w.categoryID, "Giỏ mây tre "+w.run, 10)
	for _, p := range []client.Product{pa, pb} {
		_, err := buyer.SetCartItem(ctx(t), p.Skus[0].ID, 1)
		require.NoError(t, err)
	}
	key := newKey()
	req := client.CheckoutRequest{ShippingAddress: client.DemoAddress("Người mua 2"), PaymentMethod: "COD"}

	first, err := buyer.Checkout(ctx(t), key, req, nil)
	require.NoError(t, err)
	second, err := buyer.Checkout(ctx(t), key, req, nil)
	require.NoError(t, err)
	assert.Equal(t, first.CheckoutID, second.CheckoutID)
	assert.ElementsMatch(t, []string{first.Orders[0].ID, first.Orders[1].ID}, []string{second.Orders[0].ID, second.Orders[1].ID})
	assert.Equal(t, 2, ordersOfCheckout(t, buyer, first.CheckoutID))
	assert.Equal(t, 9, stockOf(t, pa.ID, pa.Skus[0].ID), "replay reserves nothing")

	req.ShippingAddress.Line1 = "99 Đường Khác"
	_, err = buyer.Checkout(ctx(t), key, req, nil)
	assert.True(t, client.IsCode(err, "IDEMPOTENCY_KEY_REUSED"), "same key, different body: %v", err)
}

// Two concurrent submits with one key (a double click) create one set of orders.
func TestConcurrentDoubleSubmitCreatesOneCheckout(t *testing.T) {
	w := getWorld(t)
	buyer := w.buyers[3]
	require.NoError(t, buyer.ClearCart(ctx(t)))
	p := newProduct(t, w.sellerA, w.categoryID, "Chổi đót quét nhà "+w.run, 10)
	_, err := buyer.SetCartItem(ctx(t), p.Skus[0].ID, 1)
	require.NoError(t, err)
	key := newKey()
	req := client.CheckoutRequest{ShippingAddress: client.DemoAddress("Người mua 3"), PaymentMethod: "COD"}

	var wg sync.WaitGroup
	results := make([]client.CheckoutResponse, 2)
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); results[i], errs[i] = buyer.Checkout(ctx(t), key, req, nil) }()
	}
	wg.Wait()

	checkouts := map[string]bool{}
	for i := range 2 {
		if errs[i] != nil {
			assert.Equal(t, http.StatusConflict, client.StatusOf(errs[i]), "the loser sees 'in progress': %v", errs[i])
			continue
		}
		checkouts[results[i].CheckoutID] = true
	}
	require.Len(t, checkouts, 1, "exactly one checkout id across both submits")
	page, err := buyer.Orders(ctx(t), "")
	require.NoError(t, err)
	n := 0
	for _, o := range page.Items {
		if len(o.Items) > 0 && o.Items[0].ProductID == p.ID {
			n++
		}
	}
	assert.Equal(t, 1, n)
	assert.Equal(t, 9, stockOf(t, p.ID, p.Skus[0].ID))
}
