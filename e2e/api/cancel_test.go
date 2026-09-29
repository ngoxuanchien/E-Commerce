package api_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/e2e/client"
)

// Success criterion 4: cancelling returns stock. Release is asynchronous
// (outbox → Kafka → catalog consumer); WS6 §3 allows up to 5 s.
func TestCancelReturnsStock(t *testing.T) {
	w := getWorld(t)
	buyer := w.buyers[1]
	require.NoError(t, buyer.ClearCart(ctx(t)))
	p := newProduct(t, w.sellerA, w.categoryID, "Nồi đất kho cá "+w.run, 5)
	sku := p.Skus[0]

	place := func(qty int) client.Order {
		_, err := buyer.SetCartItem(ctx(t), sku.ID, qty)
		require.NoError(t, err)
		res, err := buyer.Checkout(ctx(t), newKey(), client.CheckoutRequest{SkuIDs: []string{sku.ID}, ShippingAddress: client.DemoAddress("Người mua 1"), PaymentMethod: "COD"}, nil)
		require.NoError(t, err)
		return res.Orders[0]
	}

	// Buyer cancels a pending order.
	o := place(2)
	assert.Equal(t, 3, stockOf(t, p.ID, sku.ID))
	o, err := buyer.CancelOrder(ctx(t), o.ID, "Đổi ý")
	require.NoError(t, err)
	assert.Equal(t, "CANCELLED", o.Status)
	assert.Eventually(t, func() bool { return stockOf(t, p.ID, sku.ID) == 5 }, 5*time.Second, 200*time.Millisecond, "buyer cancel returns stock")

	// Seller cancels a confirmed order.
	o = place(1)
	_, err = w.sellerA.SellerOrderAction(ctx(t), o.ID, "confirm", "")
	require.NoError(t, err)
	_, err = buyer.CancelOrder(ctx(t), o.ID, "Muộn rồi")
	assert.True(t, client.IsCode(err, "INVALID_STATE_TRANSITION"), "buyer cannot cancel a confirmed order: %v", err)
	o, err = w.sellerA.SellerOrderAction(ctx(t), o.ID, "cancel", "Hết hàng mẫu")
	require.NoError(t, err)
	assert.Equal(t, "CANCELLED", o.Status)
	assert.Eventually(t, func() bool { return stockOf(t, p.ID, sku.ID) == 5 }, 5*time.Second, 200*time.Millisecond, "seller cancel returns stock")

	// Stock never overshoots: releasing is idempotent.
	time.Sleep(2 * time.Second)
	assert.Equal(t, 5, stockOf(t, p.ID, sku.ID))
}
