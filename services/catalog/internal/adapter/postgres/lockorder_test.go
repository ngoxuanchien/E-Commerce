package postgres_test

import (
	"bytes"
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/adapter/postgres"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/testsupport"
)

// A seller PATCH listing SKUs in descending id order must not deadlock with
// ReserveStock, which locks SKUs in ascending id order.
func TestUpdateProductSkusDoesNotDeadlockWithReserve(t *testing.T) {
	store := postgres.New(testsupport.DB(t))
	ctx := context.Background()
	shop := openShop(t, store)
	p := createProduct(t, store, shop.ID, activeChange("Áo thun cotton", 100_000, 120_000))
	skus := slices.Clone(p.Skus)
	slices.SortFunc(skus, func(a, b domain.Sku) int { return bytes.Compare(b.ID[:], a.ID[:]) }) // descending
	patch := domain.ProductChange{SetSkus: true}
	for _, s := range skus {
		patch.Skus = append(patch.Skus, domain.SkuChange{ID: &s.ID, Name: s.Name, Price: s.Price, Stock: 100_000, Active: true})
	}
	items, err := domain.NormalizeStockItems([]domain.StockItem{{SkuID: skus[0].ID, Quantity: 1}, {SkuID: skus[1].ID, Quantity: 1}})
	require.NoError(t, err)

	var mu sync.Mutex
	var failures []error
	record := func(err error) {
		if err != nil {
			mu.Lock()
			failures = append(failures, err)
			mu.Unlock()
		}
	}
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(2)
		go func() { defer wg.Done(); record(store.UpdateProduct(ctx, shop.ID, p.ID, patch)) }()
		go func() { defer wg.Done(); record(store.ReserveStock(ctx, testsupport.NewID(t), items)) }()
	}
	wg.Wait()
	assert.Empty(t, failures)
}
