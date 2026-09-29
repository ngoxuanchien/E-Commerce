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
