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

func TestCreateProductPersistsEverything(t *testing.T) {
	store := postgres.New(testsupport.DB(t))
	shop := openShop(t, store)
	c := activeChange("Áo thun cotton", 150_000, 120_000)
	c.Skus = append(c.Skus, domain.SkuChange{Name: "Hidden", Price: 1_000, Stock: 1, Active: false})
	keys := []string{domain.ImageKeyPrefix(shop.ID) + uuid.NewString() + ".jpg", domain.ImageKeyPrefix(shop.ID) + uuid.NewString() + ".png"}
	c.SetImages, c.ImageKeys = true, keys

	p := createProduct(t, store, shop.ID, c)

	assert.Equal(t, "Áo thun cotton", p.Name)
	assert.Equal(t, domain.StatusActive, p.Status)
	assert.Equal(t, shop.ID, p.Shop.ID)
	assert.Equal(t, shop.Slug, p.Shop.Slug)
	assert.Equal(t, shop.OwnerUserID, p.OwnerUserID)
	assert.Equal(t, testsupport.CategoryAoThunNam, p.CategoryID)
	assert.Equal(t, keys, p.ImageKeys)
	require.Len(t, p.Skus, 3)
	assert.Equal(t, []string{"Variant 0", "Variant 1", "Hidden"}, []string{p.Skus[0].Name, p.Skus[1].Name, p.Skus[2].Name})
	require.NotNil(t, p.MinPrice)
	assert.Equal(t, int64(120_000), *p.MinPrice, "inactive SKUs do not count")
}

func TestCreateProductUnknownCategory(t *testing.T) {
	store := postgres.New(testsupport.DB(t))
	shop := openShop(t, store)
	c := activeChange("Áo thun cotton", 150_000)
	c.CategoryID = ptr(uuid.Must(uuid.NewV7()))
	_, err := store.CreateProduct(context.Background(), shop.ID, c)
	var ve *domain.ValidationError
	require.ErrorAs(t, err, &ve)
	assert.Equal(t, "does not exist", ve.Fields["category_id"])
}

func TestUpdateProductSkuSemantics(t *testing.T) {
	store := postgres.New(testsupport.DB(t))
	shop := openShop(t, store)
	p := createProduct(t, store, shop.ID, activeChange("Áo thun cotton", 150_000, 120_000))
	a, b := p.Skus[0], p.Skus[1]

	err := store.UpdateProduct(context.Background(), shop.ID, p.ID, domain.ProductChange{
		SetSkus: true,
		Skus: []domain.SkuChange{
			{ID: &a.ID, Name: "Size M", Price: 200_000, Stock: 3, Active: true}, // update; stock set directly
			{Name: "Size XL", Price: 180_000, Stock: 5, Active: true},           // create
		}, // b omitted → deactivated, not deleted
	})
	require.NoError(t, err)

	got := getProduct(t, store, p.ID)
	require.Len(t, got.Skus, 3)
	byID := map[uuid.UUID]domain.Sku{}
	for _, s := range got.Skus {
		byID[s.ID] = s
	}
	assert.Equal(t, domain.Sku{ID: a.ID, Name: "Size M", Price: 200_000, Stock: 3, Active: true}, byID[a.ID])
	assert.False(t, byID[b.ID].Active, "omitted SKU is deactivated")
	assert.Equal(t, "Size XL", got.Skus[2].Name, "new SKU is appended")
	require.NotNil(t, got.MinPrice)
	assert.Equal(t, int64(180_000), *got.MinPrice)
}

func TestUpdateProductDeactivatingAllSkusClearsMinPrice(t *testing.T) {
	store := postgres.New(testsupport.DB(t))
	shop := openShop(t, store)
	p := createProduct(t, store, shop.ID, activeChange("Áo thun cotton", 150_000))
	s := p.Skus[0]
	require.NoError(t, store.UpdateProduct(context.Background(), shop.ID, p.ID, domain.ProductChange{
		SetSkus: true, Skus: []domain.SkuChange{{ID: &s.ID, Name: s.Name, Price: s.Price, Stock: s.Stock, Active: false}},
	}))
	assert.Nil(t, getProduct(t, store, p.ID).MinPrice)
}

func TestUpdateProductRejectsForeignSkuAndRollsBack(t *testing.T) {
	store := postgres.New(testsupport.DB(t))
	shop := openShop(t, store)
	mine := createProduct(t, store, shop.ID, activeChange("Sản phẩm của tôi", 150_000))
	other := createProduct(t, store, openShop(t, store).ID, activeChange("Sản phẩm người khác", 90_000))
	foreign := other.Skus[0].ID

	err := store.UpdateProduct(context.Background(), shop.ID, mine.ID, domain.ProductChange{
		Name:    ptr("Tên đã đổi"),
		SetSkus: true,
		Skus:    []domain.SkuChange{{ID: &foreign, Name: "Hijack", Price: 1_000, Stock: 0, Active: true}},
	})
	var ve *domain.ValidationError
	require.ErrorAs(t, err, &ve)
	assert.Equal(t, "does not belong to this product", ve.Fields["skus[0].id"])

	assert.Equal(t, "Sản phẩm của tôi", getProduct(t, store, mine.ID).Name, "rolled back")
	assert.Equal(t, other.Skus, getProduct(t, store, other.ID).Skus, "other product untouched")
}

func TestUpdateProductComparesExpectedStock(t *testing.T) {
	store := postgres.New(testsupport.DB(t))
	shop := openShop(t, store)
	p := createProduct(t, store, shop.ID, activeChange("Áo thun cotton", 150_000, 120_000))
	a, b := p.Skus[0], p.Skus[1]
	edit := func(expectedA, expectedB int32) domain.ProductChange {
		return domain.ProductChange{
			Name:    ptr("Tên đã đổi"),
			SetSkus: true,
			Skus: []domain.SkuChange{
				{ID: &a.ID, Name: a.Name, Price: 99_000, Stock: 50, ExpectedStock: &expectedA, Active: true},
				{ID: &b.ID, Name: b.Name, Price: b.Price, Stock: 50, ExpectedStock: &expectedB, Active: true},
			},
		}
	}

	// A reservation took 2 units of b after the seller loaded the form.
	err := store.UpdateProduct(context.Background(), shop.ID, p.ID, edit(a.Stock, b.Stock+2))
	var sc *domain.StockChangedError
	require.ErrorAs(t, err, &sc)
	assert.Equal(t, []domain.StockChange{{SkuID: b.ID, Expected: b.Stock + 2, Actual: b.Stock}}, sc.Skus)
	got := getProduct(t, store, p.ID)
	assert.Equal(t, "Áo thun cotton", got.Name, "the whole PATCH rolled back")
	assert.Equal(t, p.Skus, got.Skus)

	require.NoError(t, store.UpdateProduct(context.Background(), shop.ID, p.ID, edit(a.Stock, b.Stock)))
	got = getProduct(t, store, p.ID)
	assert.Equal(t, int32(50), got.Skus[0].Stock)
	assert.Equal(t, int64(99_000), got.Skus[0].Price)
}

func TestUpdateAndArchiveAreScopedToShop(t *testing.T) {
	store := postgres.New(testsupport.DB(t))
	ctx := context.Background()
	owner := openShop(t, store)
	intruder := openShop(t, store)
	p := createProduct(t, store, owner.ID, activeChange("Áo thun cotton", 150_000))

	err := store.UpdateProduct(ctx, intruder.ID, p.ID, domain.ProductChange{Name: ptr("Stolen name")})
	assert.ErrorIs(t, err, domain.ErrNotFound)
	assert.ErrorIs(t, store.ArchiveProduct(ctx, intruder.ID, p.ID), domain.ErrNotFound)
	assert.Equal(t, domain.StatusActive, getProduct(t, store, p.ID).Status)

	require.NoError(t, store.ArchiveProduct(ctx, owner.ID, p.ID))
	assert.Equal(t, domain.StatusArchived, getProduct(t, store, p.ID).Status)
	require.NoError(t, store.ArchiveProduct(ctx, owner.ID, p.ID), "archiving twice is fine")
}

func TestUpdateProductReplacesImages(t *testing.T) {
	store := postgres.New(testsupport.DB(t))
	shop := openShop(t, store)
	c := activeChange("Áo thun cotton", 150_000)
	c.SetImages, c.ImageKeys = true, []string{domain.ImageKeyPrefix(shop.ID) + uuid.NewString() + ".jpg"}
	p := createProduct(t, store, shop.ID, c)

	require.NoError(t, store.UpdateProduct(context.Background(), shop.ID, p.ID, domain.ProductChange{SetImages: true, ImageKeys: []string{}}))
	assert.Empty(t, getProduct(t, store, p.ID).ImageKeys)
}

func TestProductsKeepsRequestedOrderAndSkipsUnknown(t *testing.T) {
	store := postgres.New(testsupport.DB(t))
	shop := openShop(t, store)
	a := createProduct(t, store, shop.ID, activeChange("Sản phẩm A", 1_000))
	b := createProduct(t, store, shop.ID, activeChange("Sản phẩm B", 1_000))
	ps, err := store.Products(context.Background(), []uuid.UUID{b.ID, uuid.Must(uuid.NewV7()), a.ID})
	require.NoError(t, err)
	require.Len(t, ps, 2)
	assert.Equal(t, []uuid.UUID{b.ID, a.ID}, []uuid.UUID{ps[0].ID, ps[1].ID})
}

func TestListShopProductsPaginatesNewestFirst(t *testing.T) {
	store := postgres.New(testsupport.DB(t))
	ctx := context.Background()
	shop := openShop(t, store)
	createProduct(t, store, openShop(t, store).ID, activeChange("Không phải của shop", 1_000))
	var want []uuid.UUID
	for i := range 5 {
		c := activeChange("Sản phẩm số "+string(rune('A'+i)), 1_000)
		if i == 2 {
			draft := domain.StatusDraft
			c.Status = &draft
		}
		want = append([]uuid.UUID{createProduct(t, store, shop.ID, c).ID}, want...)
	}

	var got []uuid.UUID
	cursor := ""
	for pages := 0; ; pages++ {
		require.Less(t, pages, 5, "pagination must terminate")
		page, err := store.ListShopProducts(ctx, shop.ID, nil, cursor, 2)
		require.NoError(t, err)
		for _, p := range page.Items {
			got = append(got, p.ID)
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	assert.Equal(t, want, got)

	draft := domain.StatusDraft
	drafts, err := store.ListShopProducts(ctx, shop.ID, &draft, "", 10)
	require.NoError(t, err)
	require.Len(t, drafts.Items, 1)
	assert.Nil(t, drafts.NextCursor)

	_, err = store.ListShopProducts(ctx, shop.ID, nil, "garbage", 10)
	var ve *domain.ValidationError
	assert.ErrorAs(t, err, &ve)
}
