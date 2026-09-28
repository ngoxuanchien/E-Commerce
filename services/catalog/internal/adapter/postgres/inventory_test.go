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

func TestReserveAndReleaseStock(t *testing.T) {
	pool := testsupport.DB(t)
	store := postgres.New(pool)
	ctx := context.Background()
	f := testsupport.SeedSku(t, pool, 10)
	order := testsupport.NewID(t)

	require.NoError(t, store.ReserveStock(ctx, order, []domain.StockItem{{SkuID: f.SkuID, Quantity: 4}}))
	assert.Equal(t, int32(6), testsupport.Stock(t, pool, f.SkuID))

	require.NoError(t, store.ReserveStock(ctx, order, []domain.StockItem{{SkuID: f.SkuID, Quantity: 4}}), "idempotent")
	assert.Equal(t, int32(6), testsupport.Stock(t, pool, f.SkuID))

	require.NoError(t, store.ReleaseStock(ctx, order))
	require.NoError(t, store.ReleaseStock(ctx, order), "idempotent")
	assert.Equal(t, int32(10), testsupport.Stock(t, pool, f.SkuID))

	require.NoError(t, store.ReleaseStock(ctx, testsupport.NewID(t)), "unknown order is a no-op")
}

func TestReserveStockIsAllOrNothingAndReportsEveryShortage(t *testing.T) {
	pool := testsupport.DB(t)
	store := postgres.New(pool)
	ctx := context.Background()
	plenty := testsupport.SeedSku(t, pool, 5)
	scarce := testsupport.SeedSku(t, pool, 1)
	inactive := testsupport.SeedSku(t, pool, 50)
	_, err := pool.Exec(ctx, `UPDATE skus SET active = false WHERE id = $1`, inactive.SkuID)
	require.NoError(t, err)
	draft := testsupport.SeedSku(t, pool, 50)
	_, err = pool.Exec(ctx, `UPDATE products SET status = 'draft' WHERE id = $1`, draft.ProductID)
	require.NoError(t, err)
	unknown := testsupport.NewID(t)

	items, err := domain.NormalizeStockItems([]domain.StockItem{
		{SkuID: plenty.SkuID, Quantity: 2}, {SkuID: scarce.SkuID, Quantity: 2},
		{SkuID: inactive.SkuID, Quantity: 1}, {SkuID: draft.SkuID, Quantity: 1}, {SkuID: unknown, Quantity: 1},
	})
	require.NoError(t, err)
	err = store.ReserveStock(ctx, testsupport.NewID(t), items)

	var ise *domain.InsufficientStockError
	require.ErrorAs(t, err, &ise)
	assert.ElementsMatch(t, []domain.StockShortage{
		{SkuID: scarce.SkuID, Requested: 2, Available: 1},
		{SkuID: inactive.SkuID, Requested: 1, Available: 0},
		{SkuID: draft.SkuID, Requested: 1, Available: 0},
		{SkuID: unknown, Requested: 1, Available: 0},
	}, ise.Shortages)
	assert.Equal(t, int32(5), testsupport.Stock(t, pool, plenty.SkuID), "nothing reserved")
	assert.Equal(t, int32(1), testsupport.Stock(t, pool, scarce.SkuID))

	var rows int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM stock_reservations`).Scan(&rows))
	assert.Zero(t, rows)
}

func TestReleaseStockForEventProcessesEachEventOnce(t *testing.T) {
	pool := testsupport.DB(t)
	store := postgres.New(pool)
	ctx := context.Background()
	f := testsupport.SeedSku(t, pool, 10)
	order, event := testsupport.NewID(t), testsupport.NewID(t)
	require.NoError(t, store.ReserveStock(ctx, order, []domain.StockItem{{SkuID: f.SkuID, Quantity: 3}}))

	require.NoError(t, store.ReleaseStockForEvent(ctx, event, order))
	require.NoError(t, store.ReleaseStockForEvent(ctx, event, order))
	require.NoError(t, store.ReleaseStockForEvent(ctx, testsupport.NewID(t), order), "new event, already released")
	assert.Equal(t, int32(10), testsupport.Stock(t, pool, f.SkuID))

	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM processed_events`).Scan(&n))
	assert.Equal(t, 2, n)
}

func TestSkuInfos(t *testing.T) {
	pool := testsupport.DB(t)
	store := postgres.New(pool)
	ctx := context.Background()
	shop := openShop(t, store)
	c := activeChange("Áo thun cotton", 150_000, 99_000)
	c.Skus[1].Active = false
	key := domain.ImageKeyPrefix(shop.ID) + uuid.NewString() + ".webp"
	c.SetImages, c.ImageKeys = true, []string{key}
	p := createProduct(t, store, shop.ID, c)

	infos, err := store.SkuInfos(ctx, []uuid.UUID{p.Skus[0].ID, p.Skus[1].ID, testsupport.NewID(t)})
	require.NoError(t, err)
	require.Len(t, infos, 2, "unknown ids are omitted")
	byID := map[uuid.UUID]domain.SkuInfo{infos[0].SkuID: infos[0], infos[1].SkuID: infos[1]}

	assert.Equal(t, domain.SkuInfo{
		SkuID: p.Skus[0].ID, ProductID: p.ID, ProductName: "Áo thun cotton", VariantName: "Variant 0",
		ImageKey: key, Price: 150_000, Stock: 10, Available: true,
		ShopID: shop.ID, ShopName: shop.Name, ShopSlug: shop.Slug, SellerUserID: shop.OwnerUserID,
	}, byID[p.Skus[0].ID])
	assert.False(t, byID[p.Skus[1].ID].Available, "inactive SKU")

	archived := domain.StatusArchived
	require.NoError(t, store.UpdateProduct(ctx, shop.ID, p.ID, domain.ProductChange{Status: &archived}))
	infos, err = store.SkuInfos(ctx, []uuid.UUID{p.Skus[0].ID})
	require.NoError(t, err)
	require.Len(t, infos, 1)
	assert.False(t, infos[0].Available, "inactive product")
}
