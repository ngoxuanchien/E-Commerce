package routes

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDefaultTableCoversContract checks that every WS0 §4 operation resolves
// to the expected upstream and auth level through the default table.
func TestDefaultTableCoversContract(t *testing.T) {
	cases := []struct {
		method   string
		path     string
		upstream string
		level    Level
	}{
		// identity.yaml
		{"POST", "/api/auth/register", Identity, Public},
		{"POST", "/api/auth/login", Identity, Public},
		{"POST", "/api/auth/refresh", Identity, Public},
		{"POST", "/api/auth/logout", Identity, Public},
		{"GET", "/api/users/me", Identity, User},
		// catalog.yaml
		{"GET", "/api/categories", Catalog, Public},
		{"GET", "/api/products", Catalog, Public},
		{"GET", "/api/products/p1", Catalog, Public},
		{"GET", "/api/shops/nha-sach", Catalog, Public},
		{"POST", "/api/shops", Catalog, User},
		{"GET", "/api/shops/me", Catalog, Seller},
		{"PATCH", "/api/shops/me", Catalog, Seller},
		{"GET", "/api/seller/products", Catalog, Seller},
		{"POST", "/api/seller/products", Catalog, Seller},
		{"PATCH", "/api/seller/products/p1", Catalog, Seller},
		{"DELETE", "/api/seller/products/p1", Catalog, Seller},
		{"POST", "/api/seller/uploads", Catalog, Seller},
		// order.yaml
		{"GET", "/api/cart", Order, User},
		{"PUT", "/api/cart/items/s1", Order, User},
		{"DELETE", "/api/cart/items/s1", Order, User},
		{"POST", "/api/checkout", Order, User},
		{"GET", "/api/orders", Order, User},
		{"GET", "/api/orders/o1", Order, User},
		{"POST", "/api/orders/o1/cancel", Order, User},
		{"POST", "/api/orders/o1/complete", Order, User},
		{"GET", "/api/seller/orders", Order, Seller},
		{"POST", "/api/seller/orders/o1/confirm", Order, Seller},
		{"POST", "/api/seller/orders/o1/ship", Order, Seller},
		{"POST", "/api/seller/orders/o1/deliver", Order, Seller},
		{"POST", "/api/seller/orders/o1/cancel", Order, Seller},
	}
	require.Len(t, cases, 30, "must cover all 30 WS0 §4 operations")

	table := Default()
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			idx, ok := table.Match(tc.method, tc.path)
			require.True(t, ok, "expected a match")
			assert.Equal(t, tc.upstream, table[idx].Upstream)
			assert.Equal(t, tc.level, table[idx].Auth)
		})
	}
}

// TestMatchFallsBackToStricterLevel checks that requests which don't hit a
// specific row fall through to the enclosing subtree row's (stricter) level.
func TestMatchFallsBackToStricterLevel(t *testing.T) {
	cases := []struct {
		method string
		path   string
		level  Level
	}{
		{"GET", "/api/auth/login", User},
		{"POST", "/api/auth/login/extra", User},
		{"POST", "/api/categories", User},
		{"HEAD", "/api/products", User},
		{"PUT", "/api/shops/nha-sach", User},
		{"GET", "/api/shops/nha-sach/extra", User},
		{"DELETE", "/api/shops/me", Seller},
		{"GET", "/api/shops/me/anything", Seller},
	}

	table := Default()
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			idx, ok := table.Match(tc.method, tc.path)
			require.True(t, ok, "expected a match")
			assert.Equal(t, tc.level, table[idx].Auth)
		})
	}
}

// TestMatchNonCanonicalPathsKeepStricterLevel checks that a single trailing
// slash does not relax the matched level.
func TestMatchNonCanonicalPathsKeepStricterLevel(t *testing.T) {
	cases := []struct {
		method string
		path   string
		level  Level
	}{
		{"GET", "/api/shops/me/", Seller},
		{"GET", "/api/seller/products/", Seller},
		{"GET", "/api/cart/", User},
		{"GET", "/api/products/", Public},
	}

	table := Default()
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			idx, ok := table.Match(tc.method, tc.path)
			require.True(t, ok, "expected a match")
			assert.Equal(t, tc.level, table[idx].Auth)
		})
	}
}

// TestMatchRejectsUnknownPaths checks that paths outside the contract, and
// case-mismatched paths, do not match any row.
func TestMatchRejectsUnknownPaths(t *testing.T) {
	paths := []string{
		"/",
		"/api",
		"/api/",
		"/healthz",
		"/api/admin",
		"/api/seller",
		"/api/seller/other",
		"/api/categoriesx",
		"/api/cartx",
		"/api/shops2",
		"/API/cart",
		"/.well-known/jwks.json",
	}

	table := Default()
	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			_, ok := table.Match("GET", p)
			assert.False(t, ok, "expected no match")
		})
	}
}

// TestParamSegmentNeedsAValue checks that a {name} segment requires exactly
// one non-empty path segment, no more, no less.
func TestParamSegmentNeedsAValue(t *testing.T) {
	table := Table{
		{Pattern: "/a/{id}", Subtree: false, Upstream: "x", Auth: Public},
	}

	idx, ok := table.Match("GET", "/a/1")
	require.True(t, ok)
	assert.Equal(t, 0, idx)

	_, ok = table.Match("GET", "/a/1/2")
	assert.False(t, ok)

	_, ok = table.Match("GET", "/a")
	assert.False(t, ok)
}

// TestLevelString checks the human-readable form of each Level.
func TestLevelString(t *testing.T) {
	assert.Equal(t, "public", Public.String())
	assert.Equal(t, "user", User.String())
	assert.Equal(t, "seller", Seller.String())
	assert.Equal(t, "unknown", Level(99).String())
}
