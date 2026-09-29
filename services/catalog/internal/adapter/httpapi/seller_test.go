package httpapi_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/testsupport"
)

type skuResp struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Price  int64  `json:"price"`
	Stock  int32  `json:"stock"`
	Active bool   `json:"active"`
}

type productResp struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Status   string    `json:"status"`
	Skus     []skuResp `json:"skus"`
	MinPrice *int64    `json:"min_price"`
	Images   []struct {
		ObjectKey string `json:"object_key"`
		URL       string `json:"url"`
	} `json:"images"`
}

func productBody(status string, skus ...map[string]any) map[string]any {
	return map[string]any{
		"category_id": testsupport.CategoryAoThunNam.String(),
		"name":        "Áo thun cotton",
		"description": "Vải 100% cotton",
		"status":      status,
		"skus":        skus,
	}
}

func sku(name string, price int64, stock int) map[string]any {
	return map[string]any{"name": name, "price": price, "stock": stock}
}

func (h *harness) createProduct(seller user, body map[string]any) productResp {
	h.t.Helper()
	res := h.call(http.MethodPost, "/api/seller/products", &seller, body)
	require.Equal(h.t, http.StatusCreated, res.Status, string(res.Body))
	var p productResp
	res.decode(h.t, &p)
	return p
}

func (h *harness) upload(seller user, contentType string) string {
	h.t.Helper()
	res := h.call(http.MethodPost, "/api/seller/uploads", &seller, map[string]any{"content_type": contentType})
	require.Equal(h.t, http.StatusOK, res.Status, string(res.Body))
	var u struct {
		ObjectKey string `json:"object_key"`
	}
	res.decode(h.t, &u)
	return u.ObjectKey
}

func TestSellerProductLifecycle(t *testing.T) {
	h := newHarness(t)
	_, seller := h.openShop(newUser(), "lifecycle")
	key := h.upload(seller, "image/jpeg")

	body := productBody("active", sku("Size M", 150_000, 10), sku("Size L", 120_000, 5))
	body["image_keys"] = []string{key}
	p := h.createProduct(seller, body)
	require.Len(t, p.Skus, 2)
	require.NotNil(t, p.MinPrice)
	assert.Equal(t, int64(120_000), *p.MinPrice)
	require.Len(t, p.Images, 1)
	assert.Equal(t, assetBase+"/"+key, p.Images[0].URL)

	// PATCH: update M, drop L (deactivated), add XL.
	res := h.call(http.MethodPatch, "/api/seller/products/"+p.ID, &seller, map[string]any{
		"skus": []map[string]any{
			{"id": p.Skus[0].ID, "name": "Size M", "price": 140_000, "stock": 3, "expected_stock": 10},
			sku("Size XL", 160_000, 2),
		},
	})
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	var up productResp
	res.decode(t, &up)
	require.Len(t, up.Skus, 3)
	assert.Equal(t, int32(3), up.Skus[0].Stock)
	assert.False(t, up.Skus[1].Active, "omitted SKU deactivated")
	require.NotNil(t, up.MinPrice)
	assert.Equal(t, int64(140_000), *up.MinPrice)

	var pub productResp
	h.call(http.MethodGet, "/api/products/"+p.ID, nil, nil).decode(t, &pub)
	assert.Len(t, pub.Skus, 2, "public detail hides the inactive SKU")

	assert.Equal(t, http.StatusNoContent, h.call(http.MethodDelete, "/api/seller/products/"+p.ID, &seller, nil).Status)
	assert.Equal(t, http.StatusNotFound, h.call(http.MethodGet, "/api/products/"+p.ID, nil, nil).Status)
	res = h.call(http.MethodGet, "/api/seller/products?status=archived", &seller, nil)
	require.Equal(t, http.StatusOK, res.Status)
	var page struct {
		Items []productResp `json:"items"`
	}
	res.decode(t, &page)
	require.Len(t, page.Items, 1)
	assert.Equal(t, "archived", page.Items[0].Status)
}

func TestSellerPatchComparesExpectedStock(t *testing.T) {
	h := newHarness(t)
	_, seller := h.openShop(newUser(), "stale-form")
	p := h.createProduct(seller, productBody("active", sku("Size M", 150_000, 10)))
	patch := func(fields map[string]any) result {
		s := map[string]any{"id": p.Skus[0].ID, "name": "Size M", "price": 150_000, "stock": 20}
		for k, v := range fields {
			s[k] = v
		}
		return h.call(http.MethodPatch, "/api/seller/products/"+p.ID, &seller, map[string]any{"skus": []map[string]any{s}})
	}

	res := patch(nil)
	assert.Equal(t, http.StatusBadRequest, res.Status)
	assert.Contains(t, res.fields(t), "skus[0].expected_stock")

	res = patch(map[string]any{"expected_stock": 12})
	require.Equal(t, http.StatusConflict, res.Status, string(res.Body))
	assert.Equal(t, "CONFLICT", res.code(t))
	var body struct {
		Error struct {
			Details struct {
				Skus []map[string]any `json:"skus"`
			} `json:"details"`
		} `json:"error"`
	}
	res.decode(t, &body)
	assert.Equal(t, []map[string]any{{"sku_id": p.Skus[0].ID, "expected_stock": 12.0, "stock": 10.0}}, body.Error.Details.Skus)

	res = patch(map[string]any{"expected_stock": 10})
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	var up productResp
	res.decode(t, &up)
	assert.Equal(t, int32(20), up.Skus[0].Stock)
}

func TestSellerCannotTouchAnotherShopsProduct(t *testing.T) {
	h := newHarness(t)
	_, owner := h.openShop(newUser(), "owner-shop")
	_, intruder := h.openShop(newUser(), "intruder-shop")
	p := h.createProduct(owner, productBody("active", sku("M", 150_000, 10)))
	theirs := h.createProduct(intruder, productBody("active", sku("M", 90_000, 1)))

	assert.Equal(t, http.StatusNotFound, h.call(http.MethodPatch, "/api/seller/products/"+p.ID, &intruder, map[string]any{"name": "Tên bị đổi"}).Status)
	assert.Equal(t, http.StatusNotFound, h.call(http.MethodDelete, "/api/seller/products/"+p.ID, &intruder, nil).Status)

	// A SKU id from another shop's product is rejected and nothing changes.
	res := h.call(http.MethodPatch, "/api/seller/products/"+theirs.ID, &intruder, map[string]any{
		"name": "Tên mới đây",
		"skus": []map[string]any{{"id": p.Skus[0].ID, "name": "Hijack", "price": 1_000, "stock": 0, "expected_stock": 10}},
	})
	assert.Equal(t, http.StatusBadRequest, res.Status)
	assert.Contains(t, res.fields(t), "skus[0].id")

	var got productResp
	h.call(http.MethodGet, "/api/products/"+p.ID, nil, nil).decode(t, &got)
	assert.Equal(t, "Áo thun cotton", got.Name)
	assert.Equal(t, int64(150_000), got.Skus[0].Price)
	h.call(http.MethodGet, "/api/products/"+theirs.ID, nil, nil).decode(t, &got)
	assert.Equal(t, "Áo thun cotton", got.Name, "rolled back")
}

func TestSellerProductValidation(t *testing.T) {
	h := newHarness(t)
	_, seller := h.openShop(newUser(), "validation")
	_, other := h.openShop(newUser(), "other-shop")
	foreignKey := h.upload(other, "image/png")

	body := productBody("active", sku("M", 150_000, 10))
	body["image_keys"] = []string{foreignKey}
	res := h.call(http.MethodPost, "/api/seller/products", &seller, body)
	assert.Equal(t, http.StatusBadRequest, res.Status)
	assert.Contains(t, res.fields(t), "image_keys[0]")

	body = productBody("active", sku("M", 150_000, 10))
	body["category_id"] = uuid.NewString()
	res = h.call(http.MethodPost, "/api/seller/products", &seller, body)
	assert.Equal(t, http.StatusBadRequest, res.Status)
	assert.Equal(t, "does not exist", res.fields(t)["category_id"])

	res = h.raw(http.MethodPost, "/api/seller/products", &seller, `{"name":"Áo","status":"active","skus":[{"name":"M","price":10,"stock":1}]}`)
	assert.Equal(t, http.StatusBadRequest, res.Status)
	f := res.fields(t)
	for _, k := range []string{"category_id", "name", "skus[0].price"} {
		assert.Contains(t, f, k)
	}

	p := h.createProduct(seller, productBody("draft", sku("M", 150_000, 10)))
	assert.Equal(t, http.StatusBadRequest, h.raw(http.MethodPatch, "/api/seller/products/"+p.ID, &seller, `{}`).Status)
	assert.Equal(t, http.StatusNotFound, h.raw(http.MethodPatch, "/api/seller/products/not-a-uuid", &seller, `{"name":"Tên mới đây"}`).Status)
}

func TestSellerAuthorization(t *testing.T) {
	h := newHarness(t)
	buyer := newUser()
	assert.Equal(t, http.StatusForbidden, h.call(http.MethodGet, "/api/seller/products", &buyer, nil).Status)
	assert.Equal(t, http.StatusUnauthorized, h.raw(http.MethodGet, "/api/seller/products", nil, "").Status)

	noShop := newUser().seller()
	res := h.call(http.MethodPost, "/api/seller/products", &noShop, productBody("active", sku("M", 150_000, 1)))
	assert.Equal(t, http.StatusForbidden, res.Status)
	assert.Equal(t, "FORBIDDEN", res.code(t))
	assert.Equal(t, http.StatusForbidden, h.call(http.MethodPost, "/api/seller/uploads", &noShop, map[string]any{"content_type": "image/png"}).Status)
}

func TestSellerListPaginationAndFilters(t *testing.T) {
	h := newHarness(t)
	_, seller := h.openShop(newUser(), "paging")
	var want []string
	for range 5 {
		want = append([]string{h.createProduct(seller, productBody("active", sku("M", 150_000, 1))).ID}, want...)
	}

	var got []string
	path := "/api/seller/products?limit=2"
	for pages := 0; ; pages++ {
		require.Less(t, pages, 5)
		res := h.call(http.MethodGet, path, &seller, nil)
		require.Equal(t, http.StatusOK, res.Status, string(res.Body))
		var page struct {
			Items      []productResp `json:"items"`
			NextCursor *string       `json:"next_cursor"`
		}
		res.decode(t, &page)
		for _, p := range page.Items {
			got = append(got, p.ID)
		}
		if page.NextCursor == nil {
			break
		}
		path = "/api/seller/products?" + url.Values{"limit": {"2"}, "cursor": {*page.NextCursor}}.Encode()
	}
	assert.Equal(t, want, got)

	assert.Equal(t, http.StatusBadRequest, h.raw(http.MethodGet, "/api/seller/products?status=sold", &seller, "").Status)
	assert.Equal(t, http.StatusBadRequest, h.raw(http.MethodGet, "/api/seller/products?limit=0", &seller, "").Status)
	assert.Equal(t, http.StatusBadRequest, h.raw(http.MethodGet, "/api/seller/products?cursor=zzz", &seller, "").Status)
}

func TestCreateUpload(t *testing.T) {
	h := newHarness(t)
	shop, seller := h.openShop(newUser(), "uploads")
	res := h.call(http.MethodPost, "/api/seller/uploads", &seller, map[string]any{"content_type": "image/webp"})
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	var u struct {
		ObjectKey string `json:"object_key"`
		PublicURL string `json:"public_url"`
	}
	res.decode(t, &u)
	assert.Contains(t, u.ObjectKey, "shops/"+shop.ID+"/")
	assert.Equal(t, assetBase+"/"+u.ObjectKey, u.PublicURL)

	res = h.raw(http.MethodPost, "/api/seller/uploads", &seller, `{"content_type":"image/gif"}`)
	assert.Equal(t, http.StatusBadRequest, res.Status)
	assert.Contains(t, res.fields(t), "content_type")
}
