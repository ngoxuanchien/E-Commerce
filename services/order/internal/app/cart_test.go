package app_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/catalog/catalogfake"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

func TestCartService(t *testing.T) {
	e := newEnv(t, envOpts{redis: true})
	svc := app.NewCartService(e.carts, e.catalog, nil)

	t.Run("empty cart does not call catalog", func(t *testing.T) {
		before := e.fake.Calls(catalogfake.GetSkus)
		cart, err := svc.Get(ctx, uid())
		require.NoError(t, err)
		assert.Equal(t, domain.Cart{}, cart)
		assert.Equal(t, before, e.fake.Calls(catalogfake.GetSkus))
	})

	t.Run("set, update and remove items grouped by shop", func(t *testing.T) {
		buyer, shop := uid(), catalogfake.NewShop("Shop Cart")
		a := e.fake.AddSku(shop, 100_000, 3)
		b := e.fake.AddSku(shop, 50_000, 10)

		cart, err := svc.SetItem(ctx, buyer, a.GetSkuId(), 5)
		require.NoError(t, err)
		require.Len(t, cart.Shops, 1)
		assert.Equal(t, int32(5), cart.Shops[0].Items[0].Quantity)
		assert.Equal(t, int32(3), cart.Shops[0].Items[0].Stock, "more than stock is allowed and visible")
		assert.Equal(t, int64(500_000), cart.Subtotal)

		_, err = svc.SetItem(ctx, buyer, b.GetSkuId(), 1)
		require.NoError(t, err)
		cart, err = svc.SetItem(ctx, buyer, a.GetSkuId(), 2)
		require.NoError(t, err)
		assert.Equal(t, int64(250_000), cart.Subtotal)

		cart, err = svc.RemoveItem(ctx, buyer, b.GetSkuId())
		require.NoError(t, err)
		require.Len(t, cart.Shops[0].Items, 1)
		assert.Equal(t, int64(200_000), cart.Subtotal)
	})

	t.Run("rejects unknown, unavailable, own-shop SKUs and bad quantities", func(t *testing.T) {
		buyer := uid()
		_, err := svc.SetItem(ctx, buyer, uid(), 1)
		assert.ErrorIs(t, err, app.ErrSkuNotFound)

		off := e.fake.AddSku(catalogfake.NewShop("Off"), 10_000, 5)
		e.fake.SetAvailable(off.GetSkuId(), false)
		_, err = svc.SetItem(ctx, buyer, off.GetSkuId(), 1)
		assert.ErrorIs(t, err, app.ErrSkuUnavailable)

		mine := catalogfake.NewShop("Mine")
		mine.SellerUserID = buyer
		own := e.fake.AddSku(mine, 10_000, 5)
		_, err = svc.SetItem(ctx, buyer, own.GetSkuId(), 1)
		assert.ErrorIs(t, err, app.ErrOwnShop)

		ok := e.fake.AddSku(catalogfake.NewShop("Ok"), 10_000, 5)
		for _, q := range []int32{0, 100, -1} {
			_, err = svc.SetItem(ctx, buyer, ok.GetSkuId(), q)
			assert.ErrorIs(t, err, app.ErrInvalidQuantity, fmt.Sprint(q))
		}
		lines, err := e.carts.Lines(ctx, buyer)
		require.NoError(t, err)
		assert.Empty(t, lines, "nothing was stored")
	})

	t.Run("a SKU that became unavailable is flagged and excluded from subtotal", func(t *testing.T) {
		buyer := uid()
		s := e.fake.AddSku(catalogfake.NewShop("Flag"), 10_000, 5)
		_, err := svc.SetItem(ctx, buyer, s.GetSkuId(), 2)
		require.NoError(t, err)
		e.fake.SetAvailable(s.GetSkuId(), false)

		cart, err := svc.Get(ctx, buyer)
		require.NoError(t, err)
		assert.False(t, cart.Shops[0].Items[0].Available)
		assert.Zero(t, cart.Subtotal)
	})

	t.Run("SKUs unknown to catalog are omitted and pruned", func(t *testing.T) {
		buyer, ghost := uid(), uid()
		s := e.fake.AddSku(catalogfake.NewShop("Prune"), 10_000, 5)
		e.cartSet(t, buyer, s.GetSkuId(), 1)
		e.cartSet(t, buyer, ghost, 1)

		cart, err := svc.Get(ctx, buyer)
		require.NoError(t, err)
		require.Len(t, cart.Shops, 1)
		assert.Len(t, cart.Shops[0].Items, 1)
		lines, err := e.carts.Lines(ctx, buyer)
		require.NoError(t, err)
		assert.NotContains(t, lines, ghost)
	})

	t.Run("catalog down is ErrCatalogUnavailable", func(t *testing.T) {
		buyer := uid()
		e.cartSet(t, buyer, uid(), 1)
		e.fake.FailNext(catalogfake.GetSkus, codes.Unavailable, 3)
		_, err := svc.Get(ctx, buyer)
		assert.ErrorIs(t, err, app.ErrCatalogUnavailable)
	})

	t.Run("the 51st distinct line is rejected", func(t *testing.T) {
		buyer, shop := uid(), catalogfake.NewShop("Full")
		for range domain.MaxCartLines {
			e.cartSet(t, buyer, e.fake.AddSku(shop, 1_000, 5).GetSkuId(), 1)
		}
		_, err := svc.SetItem(ctx, buyer, e.fake.AddSku(shop, 1_000, 5).GetSkuId(), 1)
		assert.ErrorIs(t, err, app.ErrCartFull)
	})
}
