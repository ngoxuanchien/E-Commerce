package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

func TestBuildCartGroupsSortsAndSumsAvailableLines(t *testing.T) {
	skus := map[string]domain.Sku{
		"a1": {ID: "a1", ProductName: "Zeta", VariantName: "M", Price: 10_000, Stock: 5, Available: true, ShopID: "shop2", ShopName: "Alpha", ShopSlug: "alpha"},
		"a2": {ID: "a2", ProductName: "Beta", VariantName: "L", Price: 20_000, Stock: 0, Available: false, ShopID: "shop2", ShopName: "Alpha", ShopSlug: "alpha"},
		"b1": {ID: "b1", ProductName: "Gamma", VariantName: "S", Price: 5_000, Stock: -3, Available: true, ShopID: "shop1", ShopName: "Bravo", ShopSlug: "bravo", ImageURL: "https://img/b1"},
	}
	cart, missing := domain.BuildCart(map[string]int32{"a1": 2, "a2": 1, "b1": 4, "zz": 1}, skus, 30_000)

	assert.Equal(t, []string{"zz"}, missing)
	assert.Equal(t, int64(2*10_000+4*5_000), cart.Subtotal, "unavailable lines do not count")
	require.Len(t, cart.Shops, 2)

	alpha := cart.Shops[0]
	assert.Equal(t, domain.ShopRef{ID: "shop2", Name: "Alpha", Slug: "alpha"}, alpha.Shop)
	require.Len(t, alpha.Items, 2)
	assert.Equal(t, "a2", alpha.Items[0].SkuID, "Beta sorts before Zeta")
	assert.False(t, alpha.Items[0].Available)
	assert.Nil(t, alpha.Items[0].ImageURL)
	assert.Equal(t, int64(30_000), alpha.ShippingFee, "every shop carries the flat fee its order will be charged")

	bravo := cart.Shops[1]
	assert.Equal(t, domain.CartItem{
		SkuID: "b1", ProductName: "Gamma", VariantName: "S", ImageURL: ptr("https://img/b1"),
		UnitPrice: 5_000, Quantity: 4, Stock: 0, Available: true,
	}, bravo.Items[0], "negative stock is clamped to 0")
}

func TestBuildCartEmpty(t *testing.T) {
	cart, missing := domain.BuildCart(nil, nil, 30_000)
	assert.Empty(t, cart.Shops)
	assert.Zero(t, cart.Subtotal)
	assert.Empty(t, missing)
}
