package main

import (
	"os"
	"path/filepath"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The dataset must satisfy WS6 §2 and the catalog validation rules (WS2 §3),
// or seeding fails halfway against the real stack.
func TestDatasetFollowsSpecAndCatalogRules(t *testing.T) {
	require.Len(t, sellers, 3)
	require.Len(t, buyers, 2)
	slugs := map[string]bool{}
	for _, s := range sellers {
		assert.Regexp(t, `^[a-z0-9-]{3,50}$`, s.Shop.Slug)
		assert.False(t, slugs[s.Shop.Slug], "duplicate slug %s", s.Shop.Slug)
		slugs[s.Shop.Slug] = true
		assert.GreaterOrEqual(t, len(s.Products), 6, s.Shop.Slug)
		assert.LessOrEqual(t, len(s.Products), 8, s.Shop.Slug)
		names := map[string]bool{}
		for _, p := range s.Products {
			assert.False(t, names[p.Name], "duplicate product %s", p.Name)
			names[p.Name] = true
			n := utf8.RuneCountInString(p.Name)
			assert.True(t, n >= 5 && n <= 200, "name length %q", p.Name)
			assert.NotEmpty(t, p.Category, p.Name)
			assert.True(t, len(p.Skus) >= 1 && len(p.Skus) <= 4, "sku count %q", p.Name)
			for _, sku := range p.Skus {
				assert.True(t, sku.Price >= 1_000 && sku.Price <= 1_000_000_000, "price %q", p.Name)
				assert.True(t, sku.Stock >= 0 && sku.Stock <= 100_000, "stock %q", p.Name)
			}
			_, err := os.Stat(filepath.Join("..", "..", "fixtures", "images", p.Image))
			assert.NoError(t, err, "image for %q", p.Name)
		}
	}
	statuses := map[string]bool{}
	for _, o := range orderPlans {
		require.Less(t, o.Buyer, len(buyers))
		require.Less(t, o.Shop, len(sellers))
		require.Less(t, o.Product, len(sellers[o.Shop].Products))
		statuses[o.Want] = true
	}
	assert.Equal(t, map[string]bool{"PENDING_CONFIRMATION": true, "CONFIRMED": true, "SHIPPING": true, "COMPLETED": true, "CANCELLED": true}, statuses)
}

// Online orders must reference valid buyers, shops, products, and match the spec.
func TestOnlinePlansFollowSpec(t *testing.T) {
	require.Len(t, onlinePlans, 6, "must have 6 online order plans per spec")

	// Verify index bounds and data types
	for i, op := range onlinePlans {
		require.Less(t, op.Buyer, len(buyers), "plan %d: buyer index out of bounds", i)
		require.Less(t, op.Shop, len(sellers), "plan %d: shop index out of bounds", i)
		require.Less(t, op.Product, len(sellers[op.Shop].Products), "plan %d: product index out of bounds", i)
		assert.Contains(t, []string{"VNPAY", "STRIPE"}, op.Method, "plan %d: Method must be VNPAY or STRIPE", i)
		assert.Contains(t, []string{"UNPAID", "PAID", "REFUNDED"}, op.WantPayment, "plan %d: WantPayment must be UNPAID, PAID, or REFUNDED", i)
	}

	// Ensure at least one COMPLETED order is on shop 0 (for seller 1 balance)
	hasCompletedOnShop0 := false
	for _, op := range onlinePlans {
		if op.Shop == 0 && op.Want == "COMPLETED" {
			hasCompletedOnShop0 = true
			break
		}
	}
	assert.True(t, hasCompletedOnShop0, "at least one COMPLETED order must be on shop 0")

	// Products must not collide with P1 orderPlans
	usedInP1 := map[string]bool{}
	for _, p := range orderPlans {
		key := sellers[p.Shop].Products[p.Product].Name
		usedInP1[key] = true
	}
	for i, op := range onlinePlans {
		productName := sellers[op.Shop].Products[op.Product].Name
		assert.False(t, usedInP1[productName], "plan %d: product %q already used in P1 orders", i, productName)
	}
}
