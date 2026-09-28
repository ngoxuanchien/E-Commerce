package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type shopResp struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	OwnerUserID string `json:"owner_user_id"`
}

// openShop opens a shop for u and returns it with u upgraded to seller.
func (h *harness) openShop(u user, slug string) (shopResp, user) {
	h.t.Helper()
	res := h.call(http.MethodPost, "/api/shops", &u, map[string]any{"name": "Shop " + slug, "slug": slug})
	require.Equal(h.t, http.StatusCreated, res.Status, string(res.Body))
	var s shopResp
	res.decode(h.t, &s)
	return s, u.seller()
}

func TestCreateShop(t *testing.T) {
	h := newHarness(t)
	u := newUser()

	res := h.call(http.MethodPost, "/api/shops", &u, map[string]any{"name": "Shop Áo Đẹp", "slug": "ao-dep", "description": "Chuyên áo thun"})
	require.Equal(t, http.StatusCreated, res.Status, string(res.Body))
	var s shopResp
	res.decode(t, &s)
	assert.Equal(t, "ao-dep", s.Slug)
	assert.Equal(t, u.ID.String(), s.OwnerUserID)

	calls := h.identity.Calls()
	require.Len(t, calls, 1)
	assert.Equal(t, u.ID.String(), calls[0].GetUserId())
	assert.Equal(t, "seller", calls[0].GetRole())

	assert.Equal(t, http.StatusOK, h.call(http.MethodGet, "/api/shops/ao-dep", nil, nil).Status)
}

func TestCreateShopErrors(t *testing.T) {
	h := newHarness(t)
	owner := newUser()
	h.openShop(owner, "taken-slug")
	other := newUser()

	res := h.raw(http.MethodPost, "/api/shops", nil, `{"name":"Shop","slug":"abc"}`)
	assert.Equal(t, http.StatusUnauthorized, res.Status, "anonymous")

	res = h.call(http.MethodPost, "/api/shops", &other, map[string]any{"name": "Shop", "slug": "taken-slug"})
	assert.Equal(t, http.StatusConflict, res.Status)
	assert.Equal(t, "CONFLICT", res.code(t))

	res = h.call(http.MethodPost, "/api/shops", &owner, map[string]any{"name": "Shop Hai", "slug": "second-shop"})
	assert.Equal(t, http.StatusConflict, res.Status, "one shop per user")

	res = h.call(http.MethodPost, "/api/shops", &other, map[string]any{"name": "Shop", "slug": "admin"})
	assert.Equal(t, http.StatusBadRequest, res.Status)
	assert.Contains(t, res.fields(t), "slug")

	res = h.raw(http.MethodPost, "/api/shops", &other, `{"name":"Shop","slug":"Bad_Slug"}`)
	assert.Equal(t, http.StatusBadRequest, res.Status)
	assert.Contains(t, res.fields(t), "slug")

	res = h.raw(http.MethodPost, "/api/shops", &other, `{"name":"Shop","slug":"abc","extra":1}`)
	assert.Equal(t, http.StatusBadRequest, res.Status, "unknown fields are rejected")

	req, _ := http.NewRequest(http.MethodPost, h.url+"/api/shops", nil)
	req.Header.Set("X-User-Id", "not-a-uuid")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "non-UUID X-User-Id is unauthenticated")
}

func TestCreateShopIdentityFailureIs503AndCreatesNothing(t *testing.T) {
	h := newHarness(t)
	h.identity.SetError(status.Error(codes.Unavailable, "down"))
	u := newUser()

	res := h.call(http.MethodPost, "/api/shops", &u, map[string]any{"name": "Shop Áo", "slug": "shop-ao"})
	assert.Equal(t, http.StatusServiceUnavailable, res.Status)
	assert.Equal(t, "UPSTREAM_UNAVAILABLE", res.code(t))
	assert.Equal(t, http.StatusNotFound, h.call(http.MethodGet, "/api/shops/shop-ao", nil, nil).Status)
}

func TestMyShop(t *testing.T) {
	h := newHarness(t)
	buyer := newUser()
	assert.Equal(t, http.StatusForbidden, h.call(http.MethodGet, "/api/shops/me", &buyer, nil).Status, "requires role seller")

	sellerNoShop := newUser().seller()
	assert.Equal(t, http.StatusNotFound, h.call(http.MethodGet, "/api/shops/me", &sellerNoShop, nil).Status)

	shop, seller := h.openShop(newUser(), "my-shop")
	res := h.call(http.MethodGet, "/api/shops/me", &seller, nil)
	require.Equal(t, http.StatusOK, res.Status)
	var got shopResp
	res.decode(t, &got)
	assert.Equal(t, shop.ID, got.ID)

	res = h.call(http.MethodPatch, "/api/shops/me", &seller, map[string]any{"description": "Mô tả mới"})
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	res.decode(t, &got)
	assert.Equal(t, "Mô tả mới", got.Description)
	assert.Equal(t, shop.Name, got.Name)

	assert.Equal(t, http.StatusBadRequest, h.raw(http.MethodPatch, "/api/shops/me", &seller, `{}`).Status)
	assert.Equal(t, http.StatusBadRequest, h.raw(http.MethodPatch, "/api/shops/me", &seller, `{"slug":"new-slug"}`).Status, "slug is immutable")
}
