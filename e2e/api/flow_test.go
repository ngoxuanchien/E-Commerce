package api_test

import (
	"net/http"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/e2e/client"
)

// Success criteria 1–4: search (with and without diacritics), a cart from two
// shops, checkout into one COD order per shop, the full seller/buyer
// lifecycle, and a presigned image upload served back from MinIO.
func TestCheckoutAcrossTwoShopsAndLifecycle(t *testing.T) {
	w := getWorld(t)
	buyer := w.buyers[0]
	require.NoError(t, buyer.ClearCart(ctx(t)))

	// Seller A lists a product with an uploaded image and two variants.
	up, err := w.sellerA.RequestUpload(ctx(t), "image/jpeg")
	require.NoError(t, err)
	require.NoError(t, client.UploadFile(ctx(t), http.DefaultClient, up, filepath.Join("..", "fixtures", "images", "home-01.jpg"), "image/jpeg"))
	pa, err := w.sellerA.CreateProduct(ctx(t), client.ProductInput{
		CategoryID: w.categoryID, Name: "Bát gốm men lam " + w.run, Description: "Gốm vẽ tay.", Status: "active",
		ImageKeys: []string{up.ObjectKey},
		Skus: []client.SkuInput{
			{Name: "Bộ nhỏ", Price: 150_000, Stock: 10, Active: true},
			{Name: "Bộ lớn", Price: 250_000, Stock: 10, Active: true},
		},
	})
	require.NoError(t, err)
	require.Len(t, pa.Images, 1)
	img, err := http.Get(pa.Images[0].URL)
	require.NoError(t, err)
	_ = img.Body.Close()
	assert.Equal(t, http.StatusOK, img.StatusCode, "uploaded image is publicly readable")

	pb := newProduct(t, w.sellerB, w.categoryID, "Đèn ngủ gỗ thông "+w.run, 10)

	// Search without and with diacritics both find the product.
	for _, q := range []string{"bat gom men lam " + w.run, "Bát gốm " + w.run} {
		page, err := buyer.SearchProducts(ctx(t), url.Values{"q": {q}})
		require.NoError(t, err)
		require.Len(t, page.Items, 1, "query %q", q)
		assert.Equal(t, pa.ID, page.Items[0].ID)
		require.NotNil(t, page.Items[0].ImageURL)
	}
	detail, err := buyer.Product(ctx(t), pa.ID)
	require.NoError(t, err)
	assert.Len(t, detail.Skus, 2)

	// Cart with lines from two shops.
	large := pa.Skus[1]
	_, err = buyer.SetCartItem(ctx(t), large.ID, 2)
	require.NoError(t, err)
	cart, err := buyer.SetCartItem(ctx(t), pb.Skus[0].ID, 1)
	require.NoError(t, err)
	require.Len(t, cart.Shops, 2)

	// Checkout: one COD order per shop, sharing the checkout id.
	res, err := buyer.Checkout(ctx(t), newKey(), client.CheckoutRequest{ShippingAddress: client.DemoAddress("Người mua 0"), PaymentMethod: "COD"}, nil)
	require.NoError(t, err)
	require.Len(t, res.Orders, 2)
	byShop := map[string]client.Order{}
	for _, o := range res.Orders {
		assert.Equal(t, res.CheckoutID, o.CheckoutID)
		assert.Equal(t, "PENDING_CONFIRMATION", o.Status)
		assert.Equal(t, shippingFee, o.ShippingFee)
		assert.Equal(t, o.Subtotal+o.ShippingFee, o.Total)
		byShop[o.Shop.ID] = o
	}
	orderA, orderB := byShop[w.shopA.ID], byShop[w.shopB.ID]
	require.NotEmpty(t, orderA.ID)
	require.NotEmpty(t, orderB.ID)
	assert.Equal(t, 2*large.Price, orderA.Subtotal)

	cart, err = buyer.Cart(ctx(t))
	require.NoError(t, err)
	assert.Empty(t, cart.Shops, "purchased lines leave the cart")
	assert.Equal(t, 8, stockOf(t, pa.ID, large.ID), "checkout reserves stock synchronously")

	// Seller B cannot act on seller A's order; seller A walks it to DELIVERED.
	_, err = w.sellerB.SellerOrderAction(ctx(t), orderA.ID, "confirm", "")
	assert.Equal(t, http.StatusNotFound, client.StatusOf(err))
	for _, step := range []struct{ action, want string }{{"confirm", "CONFIRMED"}, {"ship", "SHIPPING"}, {"deliver", "DELIVERED"}} {
		o, err := w.sellerA.SellerOrderAction(ctx(t), orderA.ID, step.action, "")
		require.NoError(t, err, step.action)
		assert.Equal(t, step.want, o.Status)
	}
	done, err := buyer.CompleteOrder(ctx(t), orderA.ID)
	require.NoError(t, err)
	assert.Equal(t, "COMPLETED", done.Status)

	var to []string
	for _, h := range done.History {
		to = append(to, h.To)
	}
	assert.Subset(t, to, []string{"PENDING_CONFIRMATION", "CONFIRMED", "SHIPPING", "DELIVERED", "COMPLETED"})
	assert.Equal(t, "COMPLETED", to[len(to)-1])

	// Seller B sees its own order.
	sellerOrders, err := w.sellerB.SellerOrders(ctx(t), "PENDING_CONFIRMATION")
	require.NoError(t, err)
	var ids []string
	for _, o := range sellerOrders.Items {
		ids = append(ids, o.ID)
	}
	assert.Contains(t, ids, orderB.ID)
}
