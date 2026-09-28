package postgres_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/adapter/postgres"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/testsupport"
)

func ptr[T any](v T) *T { return &v }

// openShop creates a shop with a unique slug.
func openShop(t *testing.T, store *postgres.Store) domain.Shop {
	t.Helper()
	id := testsupport.NewID(t)
	shop, err := store.CreateShop(context.Background(), domain.Shop{
		ID: id, OwnerUserID: testsupport.NewID(t), Name: "Shop", Slug: "s-" + id.String()[24:],
	}, noHook)
	require.NoError(t, err)
	return shop
}

// activeChange is a valid create change: active status, category Áo thun nam,
// one active SKU per price (stock 10).
func activeChange(name string, prices ...int64) domain.ProductChange {
	skus := make([]domain.SkuChange, len(prices))
	for i, p := range prices {
		skus[i] = domain.SkuChange{Name: fmt.Sprintf("Variant %d", i), Price: p, Stock: 10, Active: true}
	}
	st := domain.StatusActive
	return domain.ProductChange{
		CategoryID: ptr(testsupport.CategoryAoThunNam), Name: ptr(name), Description: ptr(""),
		Status: &st, SetSkus: true, Skus: skus,
	}
}

func createProduct(t *testing.T, store *postgres.Store, shopID uuid.UUID, c domain.ProductChange) domain.Product {
	t.Helper()
	id, err := store.CreateProduct(context.Background(), shopID, c)
	require.NoError(t, err)
	return getProduct(t, store, id)
}

func getProduct(t *testing.T, store *postgres.Store, id uuid.UUID) domain.Product {
	t.Helper()
	ps, err := store.Products(context.Background(), []uuid.UUID{id})
	require.NoError(t, err)
	require.Len(t, ps, 1)
	return ps[0]
}
