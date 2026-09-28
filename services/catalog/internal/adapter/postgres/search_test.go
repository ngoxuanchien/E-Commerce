package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/adapter/postgres"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/testsupport"
)

func search(t *testing.T, store *postgres.Store, p domain.SearchParams) domain.Page[domain.ProductSummary] {
	t.Helper()
	p, err := p.Normalize()
	require.NoError(t, err)
	page, err := store.SearchProducts(context.Background(), p)
	require.NoError(t, err)
	return page
}

func names(page domain.Page[domain.ProductSummary]) []string {
	out := make([]string, len(page.Items))
	for i, s := range page.Items {
		out[i] = s.Name
	}
	return out
}

// searchAll follows next_cursor until the last page and returns every id.
func searchAll(t *testing.T, store *postgres.Store, p domain.SearchParams) []uuid.UUID {
	t.Helper()
	var ids []uuid.UUID
	for pages := 0; ; pages++ {
		require.Less(t, pages, 50, "pagination must terminate")
		page := search(t, store, p)
		for _, s := range page.Items {
			ids = append(ids, s.ID)
		}
		if page.NextCursor == nil {
			return ids
		}
		p.Cursor = *page.NextCursor
	}
}

func TestSearchIsAccentAndCaseInsensitive(t *testing.T) {
	store := postgres.New(testsupport.DB(t))
	shop := openShop(t, store)
	createProduct(t, store, shop.ID, activeChange("Áo thun cotton", 150_000))
	createProduct(t, store, shop.ID, activeChange("Đầm maxi đi biển", 250_000))
	createProduct(t, store, shop.ID, activeChange("Quần jeans nam", 350_000))

	assert.Equal(t, []string{"Áo thun cotton"}, names(search(t, store, domain.SearchParams{Query: "ao thun"})))
	assert.Equal(t, []string{"Áo thun cotton"}, names(search(t, store, domain.SearchParams{Query: "AO THUN"})))
	assert.Equal(t, []string{"Áo thun cotton"}, names(search(t, store, domain.SearchParams{Query: "áo thun"})))
	assert.Equal(t, []string{"Đầm maxi đi biển"}, names(search(t, store, domain.SearchParams{Query: "dam"})))
	assert.Empty(t, search(t, store, domain.SearchParams{Query: "laptop"}).Items)
}

func TestSearchRanksNameAboveDescription(t *testing.T) {
	store := postgres.New(testsupport.DB(t))
	shop := openShop(t, store)
	inDesc := activeChange("Bộ quần áo thể thao", 100_000)
	inDesc.Description = ptr("Chất liệu giống áo thun")
	createProduct(t, store, shop.ID, inDesc)
	createProduct(t, store, shop.ID, activeChange("Áo thun basic", 100_000))

	assert.Equal(t, []string{"Áo thun basic", "Bộ quần áo thể thao"}, names(search(t, store, domain.SearchParams{Query: "ao thun"})))
}

func TestSearchOnlyListsActiveProductsWithActiveSkus(t *testing.T) {
	store := postgres.New(testsupport.DB(t))
	shop := openShop(t, store)
	createProduct(t, store, shop.ID, activeChange("Sản phẩm hiển thị", 1_000))
	draft := activeChange("Sản phẩm nháp", 1_000)
	draft.Status = ptr(domain.StatusDraft)
	createProduct(t, store, shop.ID, draft)
	archived := activeChange("Sản phẩm lưu trữ", 1_000)
	archived.Status = ptr(domain.StatusArchived)
	createProduct(t, store, shop.ID, archived)
	noActive := activeChange("Sản phẩm hết biến thể", 1_000)
	noActive.Skus[0].Active = false
	createProduct(t, store, shop.ID, noActive)

	assert.Equal(t, []string{"Sản phẩm hiển thị"}, names(search(t, store, domain.SearchParams{})))
}

func TestSearchFilters(t *testing.T) {
	store := postgres.New(testsupport.DB(t))
	shopA, shopB := openShop(t, store), openShop(t, store)
	createProduct(t, store, shopA.ID, activeChange("Áo thun rẻ", 50_000)) // category ao-thun-nam
	dress := activeChange("Váy hoa nhí", 300_000)
	dress.CategoryID = ptr(testsupport.CategoryVayDam)
	createProduct(t, store, shopB.ID, dress)
	top := activeChange("Áo khoác nam", 500_000)
	top.CategoryID = ptr(testsupport.CategoryThoiTrangNam)
	createProduct(t, store, shopB.ID, top)

	byName := func(p domain.SearchParams) []string {
		p.Sort = domain.SortPriceAsc
		return names(search(t, store, p))
	}
	assert.Equal(t, []string{"Áo thun rẻ", "Áo khoác nam"}, byName(domain.SearchParams{CategorySlug: "thoi-trang-nam"}), "parent includes children")
	assert.Equal(t, []string{"Áo thun rẻ"}, byName(domain.SearchParams{CategorySlug: "ao-thun-nam"}))
	assert.Empty(t, byName(domain.SearchParams{CategorySlug: "no-such-category"}))
	assert.Equal(t, []string{"Váy hoa nhí", "Áo khoác nam"}, byName(domain.SearchParams{ShopSlug: shopB.Slug}))
	assert.Empty(t, byName(domain.SearchParams{ShopSlug: "no-such-shop"}))
	assert.Equal(t, []string{"Áo thun rẻ", "Váy hoa nhí"}, byName(domain.SearchParams{MinPrice: ptr(int64(50_000)), MaxPrice: ptr(int64(300_000))}), "bounds are inclusive")

	desc := search(t, store, domain.SearchParams{Sort: domain.SortPriceDesc})
	assert.Equal(t, []string{"Áo khoác nam", "Váy hoa nhí", "Áo thun rẻ"}, names(desc))
	newest := search(t, store, domain.SearchParams{Sort: domain.SortNewest})
	assert.Equal(t, []string{"Áo khoác nam", "Váy hoa nhí", "Áo thun rẻ"}, names(newest))
}

func TestSearchSummaryFields(t *testing.T) {
	store := postgres.New(testsupport.DB(t))
	shop := openShop(t, store)
	c := activeChange("Áo thun có ảnh", 150_000, 99_000)
	first := domain.ImageKeyPrefix(shop.ID) + uuid.NewString() + ".jpg"
	c.SetImages, c.ImageKeys = true, []string{first, domain.ImageKeyPrefix(shop.ID) + uuid.NewString() + ".png"}
	createProduct(t, store, shop.ID, c)
	createProduct(t, store, shop.ID, activeChange("Áo thun không ảnh", 1_000))

	page := search(t, store, domain.SearchParams{Sort: domain.SortPriceDesc})
	require.Len(t, page.Items, 2)
	withImage := page.Items[0]
	assert.Equal(t, first, withImage.ImageKey)
	assert.Equal(t, int64(99_000), withImage.MinPrice)
	assert.Equal(t, domain.ShopRef{ID: shop.ID, Name: shop.Name, Slug: shop.Slug}, withImage.Shop)
	assert.Empty(t, page.Items[1].ImageKey)
}

func TestSearchPaginationHasNoDuplicatesOrGapsOnTies(t *testing.T) {
	store := postgres.New(testsupport.DB(t))
	shop := openShop(t, store)
	var all []uuid.UUID
	for range 7 {
		all = append(all, createProduct(t, store, shop.ID, activeChange("Áo thun giống nhau", 50_000)).ID)
	}
	for _, sort := range []domain.SearchSort{domain.SortNewest, domain.SortPriceAsc, domain.SortPriceDesc, domain.SortRelevance} {
		t.Run(string(sort), func(t *testing.T) {
			p := domain.SearchParams{Sort: sort, Limit: 3}
			if sort == domain.SortRelevance {
				p.Query = "ao thun"
			}
			got := searchAll(t, store, p)
			assert.Len(t, got, 7)
			assert.ElementsMatch(t, all, got)
		})
	}
}

func TestSearchNeverFailsOnOddQueries(t *testing.T) {
	store := postgres.New(testsupport.DB(t))
	createProduct(t, store, openShop(t, store).ID, activeChange("Áo thun cotton", 150_000))
	for _, q := range []string{`"`, `-`, `or`, `!!!`, `'; drop table products; --`, `ao & | ! thun`, `"ao thun" -cotton`, `(`, `\`} {
		t.Run(q, func(t *testing.T) {
			search(t, store, domain.SearchParams{Query: q})
		})
	}
	assert.Len(t, search(t, store, domain.SearchParams{}).Items, 1, "table still there")
}

func TestSearchRejectsCursorFromAnotherSort(t *testing.T) {
	store := postgres.New(testsupport.DB(t))
	shop := openShop(t, store)
	for range 3 {
		createProduct(t, store, shop.ID, activeChange("Áo thun", 1_000))
	}
	page := search(t, store, domain.SearchParams{Sort: domain.SortPriceAsc, Limit: 1})
	require.NotNil(t, page.NextCursor)

	p, err := domain.SearchParams{Sort: domain.SortNewest, Cursor: *page.NextCursor}.Normalize()
	require.NoError(t, err)
	_, err = store.SearchProducts(context.Background(), p)
	var ve *domain.ValidationError
	require.ErrorAs(t, err, &ve)
	assert.Contains(t, ve.Fields, "cursor")
}
