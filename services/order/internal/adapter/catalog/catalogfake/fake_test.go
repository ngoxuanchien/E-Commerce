package catalogfake_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	catalogv1 "github.com/ngoxuanchien/e-commerce/gen/catalog/v1"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/catalog/catalogfake"
)

var ctx = context.Background()

func setup(t *testing.T) (*catalogfake.Fake, catalogv1.CatalogInternalClient) {
	f := catalogfake.New()
	return f, catalogv1.NewCatalogInternalClient(f.Dial(t))
}

func reserve(c catalogv1.CatalogInternalClient, orderID string, items ...*catalogv1.StockItem) error {
	_, err := c.ReserveStock(ctx, &catalogv1.ReserveStockRequest{OrderId: orderID, Items: items})
	return err
}

func item(skuID string, q int32) *catalogv1.StockItem {
	return &catalogv1.StockItem{SkuId: skuID, Quantity: q}
}

func shortages(t *testing.T, err error) []*catalogv1.StockShortage {
	t.Helper()
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.FailedPrecondition, st.Code())
	for _, d := range st.Details() {
		if is, ok := d.(*catalogv1.InsufficientStock); ok {
			return is.GetShortages()
		}
	}
	t.Fatalf("no InsufficientStock detail in %v", err)
	return nil
}

func TestGetSkusOmitsUnknown(t *testing.T) {
	f, c := setup(t)
	shop := catalogfake.NewShop("Shop A")
	s := f.AddSku(shop, 100_000, 5)

	resp, err := c.GetSkus(ctx, &catalogv1.GetSkusRequest{SkuIds: []string{s.GetSkuId(), "00000000-0000-7000-8000-000000000000"}})
	require.NoError(t, err)
	require.Len(t, resp.GetSkus(), 1)
	got := resp.GetSkus()[0]
	assert.Equal(t, s.GetSkuId(), got.GetSkuId())
	assert.Equal(t, "shop-a", got.GetShopSlug())
	assert.Equal(t, shop.SellerUserID, got.GetSellerUserId())
	assert.True(t, got.GetAvailable())
	assert.Equal(t, 1, f.Calls(catalogfake.GetSkus))
}

func TestReserveDecrementsStockAndIsIdempotentPerOrder(t *testing.T) {
	f, c := setup(t)
	s := f.AddSku(catalogfake.NewShop("S"), 10_000, 5)

	require.NoError(t, reserve(c, "o1", item(s.GetSkuId(), 2), item(s.GetSkuId(), 1)))
	assert.Equal(t, int32(2), f.Stock(s.GetSkuId()), "duplicate items are merged")
	require.NoError(t, reserve(c, "o1", item(s.GetSkuId(), 3)))
	assert.Equal(t, int32(2), f.Stock(s.GetSkuId()), "same order reserves once")
	assert.True(t, f.Reserved("o1"))
}

func TestReserveReportsEveryShortageAndReservesNothing(t *testing.T) {
	f, c := setup(t)
	shop := catalogfake.NewShop("S")
	a := f.AddSku(shop, 10_000, 1)
	b := f.AddSku(shop, 10_000, 1)
	off := f.AddSku(shop, 10_000, 9)
	f.SetAvailable(off.GetSkuId(), false)

	err := reserve(c, "o1", item(a.GetSkuId(), 2), item(b.GetSkuId(), 1), item(off.GetSkuId(), 1))
	got := shortages(t, err)
	want := map[string][2]int32{a.GetSkuId(): {2, 1}, off.GetSkuId(): {1, 0}}
	require.Len(t, got, 2)
	for _, s := range got {
		assert.Equal(t, want[s.GetSkuId()], [2]int32{s.GetRequested(), s.GetAvailable()})
	}
	assert.Equal(t, int32(1), f.Stock(a.GetSkuId()))
	assert.Equal(t, int32(1), f.Stock(b.GetSkuId()))
	assert.False(t, f.Reserved("o1"))
}

func TestReserveUnknownSkuIsNotFound(t *testing.T) {
	_, c := setup(t)
	err := reserve(c, "o1", item("00000000-0000-7000-8000-000000000000", 1))
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestReleaseRestoresStockOnceAndIsIdempotent(t *testing.T) {
	f, c := setup(t)
	s := f.AddSku(catalogfake.NewShop("S"), 10_000, 5)
	require.NoError(t, reserve(c, "o1", item(s.GetSkuId(), 3)))

	for range 2 {
		_, err := c.ReleaseStock(ctx, &catalogv1.ReleaseStockRequest{OrderId: "o1"})
		require.NoError(t, err)
	}
	assert.Equal(t, int32(5), f.Stock(s.GetSkuId()))
	assert.False(t, f.Reserved("o1"))
	assert.Equal(t, []string{"o1", "o1"}, f.ReleasedOrders())
}

func TestFailNextInjectsErrorsInOrder(t *testing.T) {
	f, c := setup(t)
	s := f.AddSku(catalogfake.NewShop("S"), 10_000, 5)
	f.FailNext(catalogfake.ReserveStock, codes.Unavailable, 2)

	assert.Equal(t, codes.Unavailable, status.Code(reserve(c, "o1", item(s.GetSkuId(), 1))))
	assert.Equal(t, codes.Unavailable, status.Code(reserve(c, "o1", item(s.GetSkuId(), 1))))
	require.NoError(t, reserve(c, "o1", item(s.GetSkuId(), 1)))
	assert.Equal(t, 3, f.Calls(catalogfake.ReserveStock))
}

func TestFailReserveForSkusUntilCleared(t *testing.T) {
	f, c := setup(t)
	s := f.AddSku(catalogfake.NewShop("S"), 10_000, 5)
	f.FailReserveForSkus(codes.Unavailable, s.GetSkuId())
	assert.Equal(t, codes.Unavailable, status.Code(reserve(c, "o1", item(s.GetSkuId(), 1))))
	assert.Equal(t, int32(5), f.Stock(s.GetSkuId()))

	f.FailReserveForSkus(codes.OK, s.GetSkuId())
	require.NoError(t, reserve(c, "o1", item(s.GetSkuId(), 1)))
}

func TestDelayHonoursClientDeadline(t *testing.T) {
	f, c := setup(t)
	f.SetDelay(catalogfake.GetSkus, 500*time.Millisecond)
	dctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_, err := c.GetSkus(dctx, &catalogv1.GetSkusRequest{SkuIds: []string{"x"}})
	assert.Equal(t, codes.DeadlineExceeded, status.Code(err))
}
