package catalog_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/catalog"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/catalog/catalogfake"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

var (
	ctx  = context.Background()
	fast = catalog.Config{Timeout: 100 * time.Millisecond, Retries: 2, Backoff: time.Millisecond}
)

func setup(t *testing.T, cfg catalog.Config) (*catalog.Client, *catalogfake.Fake) {
	f := catalogfake.New()
	return catalog.New(f.Dial(t), cfg), f
}

func TestDefaultConfigMatchesSpec(t *testing.T) {
	assert.Equal(t, catalog.Config{Timeout: 3 * time.Second, Retries: 2, Backoff: 100 * time.Millisecond}, catalog.DefaultConfig())
}

func TestGetSkusMapsFields(t *testing.T) {
	c, f := setup(t, fast)
	shop := catalogfake.NewShop("Shop A")
	s := f.AddSku(shop, 120_000, 7)

	got, err := c.GetSkus(ctx, []string{s.GetSkuId(), "00000000-0000-7000-8000-000000000000"})
	require.NoError(t, err)
	assert.Equal(t, map[string]domain.Sku{s.GetSkuId(): {
		ID: s.GetSkuId(), ProductID: s.GetProductId(), ProductName: s.GetProductName(), VariantName: s.GetVariantName(),
		ImageURL: s.GetImageUrl(), Price: 120_000, Stock: 7, Available: true,
		ShopID: shop.ID, ShopName: "Shop A", ShopSlug: "shop-a", SellerUserID: shop.SellerUserID,
	}}, got)
}

func TestGetSkusWithNoIDsSkipsTheCall(t *testing.T) {
	c, f := setup(t, fast)
	got, err := c.GetSkus(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Zero(t, f.Calls(catalogfake.GetSkus))
}

func TestTransientFailuresAreRetried(t *testing.T) {
	c, f := setup(t, fast)
	s := f.AddSku(catalogfake.NewShop("S"), 10_000, 5)
	f.FailNext(catalogfake.ReserveStock, codes.Unavailable, 2)

	require.NoError(t, c.ReserveStock(ctx, "o1", []domain.Line{{SkuID: s.GetSkuId(), Quantity: 1}}))
	assert.Equal(t, 3, f.Calls(catalogfake.ReserveStock))
	assert.True(t, f.Reserved("o1"))
}

func TestGivesUpAfterTwoRetries(t *testing.T) {
	c, f := setup(t, fast)
	f.FailNext(catalogfake.ReleaseStock, codes.Unavailable, 3)

	err := c.ReleaseStock(ctx, "o1")
	assert.ErrorIs(t, err, app.ErrCatalogUnavailable)
	assert.Equal(t, 3, f.Calls(catalogfake.ReleaseStock))
}

func TestPerAttemptTimeoutIsRetriedThenUnavailable(t *testing.T) {
	c, f := setup(t, catalog.Config{Timeout: 30 * time.Millisecond, Retries: 2, Backoff: time.Millisecond})
	f.SetDelay(catalogfake.GetSkus, 300*time.Millisecond)

	_, err := c.GetSkus(ctx, []string{"x"})
	assert.ErrorIs(t, err, app.ErrCatalogUnavailable)
	assert.Equal(t, 3, f.Calls(catalogfake.GetSkus))
}

func TestInsufficientStockMapsToOutOfStockWithoutRetry(t *testing.T) {
	c, f := setup(t, fast)
	s := f.AddSku(catalogfake.NewShop("S"), 10_000, 1)

	err := c.ReserveStock(ctx, "o1", []domain.Line{{SkuID: s.GetSkuId(), Quantity: 3}})
	var oos *app.OutOfStockError
	require.ErrorAs(t, err, &oos)
	assert.Equal(t, []domain.Shortage{{SkuID: s.GetSkuId(), Requested: 3, Available: 1}}, oos.Shortages)
	assert.Equal(t, 1, f.Calls(catalogfake.ReserveStock))
}

func TestNotFoundMapsToRejected(t *testing.T) {
	c, f := setup(t, fast)
	err := c.ReserveStock(ctx, "o1", []domain.Line{{SkuID: "00000000-0000-7000-8000-000000000000", Quantity: 1}})
	assert.ErrorIs(t, err, app.ErrCatalogRejected)
	assert.Equal(t, 1, f.Calls(catalogfake.ReserveStock))
}

func TestReleaseStockReturnsReservedStock(t *testing.T) {
	c, f := setup(t, fast)
	s := f.AddSku(catalogfake.NewShop("S"), 10_000, 4)
	require.NoError(t, c.ReserveStock(ctx, "o1", []domain.Line{{SkuID: s.GetSkuId(), Quantity: 4}}))
	require.NoError(t, c.ReleaseStock(ctx, "o1"))
	assert.Equal(t, int32(4), f.Stock(s.GetSkuId()))
}

func TestCancelledContextStopsRetrying(t *testing.T) {
	c, f := setup(t, catalog.Config{Timeout: time.Second, Retries: 2, Backoff: time.Second})
	f.FailNext(catalogfake.GetSkus, codes.Unavailable, 5)
	cctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := c.GetSkus(cctx, []string{"x"})
	assert.ErrorIs(t, err, app.ErrCatalogUnavailable)
	assert.Less(t, time.Since(start), 900*time.Millisecond)
}
