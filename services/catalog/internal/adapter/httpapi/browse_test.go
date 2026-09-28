package httpapi_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
)

type summaryPage struct {
	Items []struct {
		ID       string  `json:"id"`
		Name     string  `json:"name"`
		ImageURL *string `json:"image_url"`
		MinPrice int64   `json:"min_price"`
	} `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

func TestListCategories(t *testing.T) {
	h := newHarness(t)
	res := h.call(http.MethodGet, "/api/categories", nil, nil)
	require.Equal(t, http.StatusOK, res.Status)
	var tree []struct {
		Slug     string `json:"slug"`
		Children []struct {
			Slug     string `json:"slug"`
			Children []any  `json:"children"`
		} `json:"children"`
	}
	res.decode(t, &tree)
	require.Len(t, tree, 10)
	require.Len(t, tree[0].Children, 3)
	assert.NotNil(t, tree[0].Children[0].Children)
}

func TestSearchProductsFindsWithoutDiacriticsAndPaginates(t *testing.T) {
	h := newHarness(t)
	h.seedProduct("Áo thun cotton trắng", 150_000, domain.StatusActive)
	h.seedProduct("Áo thun cotton đen", 160_000, domain.StatusActive)
	h.seedProduct("Áo thun cotton xanh", 170_000, domain.StatusActive)
	h.seedProduct("Áo thun nháp", 100_000, domain.StatusDraft)

	var seen []string
	path := "/api/products?" + url.Values{"q": {"ao thun"}, "limit": {"2"}}.Encode()
	for pages := 0; ; pages++ {
		require.Less(t, pages, 5)
		res := h.call(http.MethodGet, path, nil, nil)
		require.Equal(t, http.StatusOK, res.Status, string(res.Body))
		var page summaryPage
		res.decode(t, &page)
		for _, it := range page.Items {
			seen = append(seen, it.Name)
		}
		if page.NextCursor == nil {
			break
		}
		path = "/api/products?" + url.Values{"q": {"ao thun"}, "limit": {"2"}, "cursor": {*page.NextCursor}}.Encode()
	}
	assert.ElementsMatch(t, []string{"Áo thun cotton trắng", "Áo thun cotton đen", "Áo thun cotton xanh"}, seen)
}

func TestSearchProductsOddQueriesAreNotErrors(t *testing.T) {
	h := newHarness(t)
	h.seedProduct("Áo thun cotton", 150_000, domain.StatusActive)
	for _, q := range []string{`"`, `-`, `or`, `!!!`, `'; drop table products; --`} {
		res := h.call(http.MethodGet, "/api/products?"+url.Values{"q": {q}}.Encode(), nil, nil)
		assert.Equal(t, http.StatusOK, res.Status, "q=%q: %s", q, res.Body)
	}
}

func TestSearchProductsRejectsBadParams(t *testing.T) {
	h := newHarness(t)
	cases := map[string]string{
		"limit=0":                         "limit",
		"limit=101":                       "limit",
		"limit=abc":                       "limit",
		"min_price=-1":                    "min_price",
		"min_price=abc":                   "min_price",
		"max_price=1.5":                   "max_price",
		"min_price=10&max_price=5":        "min_price",
		"sort=cheapest":                   "sort",
		"cursor=garbage":                  "cursor",
	}
	newestCursor, err := domain.EncodeCursor("newest", "2026-09-28T10:00:00Z", uuid.Must(uuid.NewV7()))
	require.NoError(t, err)
	cases[url.Values{"sort": {"price_asc"}, "cursor": {newestCursor}}.Encode()] = "cursor" // cursor from another sort
	for query, field := range cases {
		t.Run(query, func(t *testing.T) {
			res := h.raw(http.MethodGet, "/api/products?"+query, nil, "")
			require.Equal(t, http.StatusBadRequest, res.Status, string(res.Body))
			assert.Equal(t, "VALIDATION_ERROR", res.code(t))
			assert.Contains(t, res.fields(t), field)
		})
	}
}

func TestGetProductVisibility(t *testing.T) {
	h := newHarness(t)
	active := h.seedProduct("Áo thun cotton", 150_000, domain.StatusActive)
	draft := h.seedProduct("Áo thun nháp", 150_000, domain.StatusDraft)
	owner := user{ID: draft.OwnerID, Roles: "buyer,seller"}
	stranger := newUser()

	res := h.call(http.MethodGet, "/api/products/"+active.ProductID.String(), nil, nil)
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	var p struct {
		Name     string `json:"name"`
		MinPrice *int64 `json:"min_price"`
		Shop     struct {
			Slug string `json:"slug"`
		} `json:"shop"`
	}
	res.decode(t, &p)
	assert.Equal(t, "Áo thun cotton", p.Name)
	require.NotNil(t, p.MinPrice)
	assert.NotEmpty(t, p.Shop.Slug)

	assert.Equal(t, http.StatusNotFound, h.call(http.MethodGet, "/api/products/"+draft.ProductID.String(), nil, nil).Status)
	assert.Equal(t, http.StatusNotFound, h.call(http.MethodGet, "/api/products/"+draft.ProductID.String(), &stranger, nil).Status)
	assert.Equal(t, http.StatusOK, h.call(http.MethodGet, "/api/products/"+draft.ProductID.String(), &owner, nil).Status)

	res = h.raw(http.MethodGet, "/api/products/not-a-uuid", nil, "")
	assert.Equal(t, http.StatusNotFound, res.Status)
	assert.Equal(t, "NOT_FOUND", res.code(t))
}

func TestGetShopBySlug(t *testing.T) {
	h := newHarness(t)
	f := h.seedProduct("Áo thun cotton", 150_000, domain.StatusActive)
	slug := "fx-" + f.ShopID.String()[24:]

	res := h.call(http.MethodGet, "/api/shops/"+slug, nil, nil)
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))
	var shop struct {
		ID          string `json:"id"`
		Description string `json:"description"`
	}
	res.decode(t, &shop)
	assert.Equal(t, f.ShopID.String(), shop.ID)

	assert.Equal(t, http.StatusNotFound, h.call(http.MethodGet, "/api/shops/no-such-shop", nil, nil).Status)
}

func TestUnknownRouteIsJSON404(t *testing.T) {
	h := newHarness(t)
	res := h.raw(http.MethodGet, "/api/nope", nil, "")
	assert.Equal(t, http.StatusNotFound, res.Status)
	assert.Equal(t, "NOT_FOUND", res.code(t))
}
