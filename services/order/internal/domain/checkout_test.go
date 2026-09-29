package domain_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

func ptr[T any](v T) *T { return &v }

func seqIDs() func() string {
	n := 0
	return func() string { n++; return fmt.Sprintf("order-%d", n) }
}

func sku(id, shop, seller string, price int64) domain.Sku {
	return domain.Sku{
		ID: id, ProductID: "p-" + id, ProductName: "Product " + id, VariantName: "Variant " + id,
		ImageURL: "https://img/" + id, Price: price, Stock: 10, Available: true,
		ShopID: shop, ShopName: "Shop " + shop, ShopSlug: "shop-" + shop, SellerUserID: seller,
	}
}

var addr = domain.Address{FullName: "Nguyễn Văn A", Phone: "0900000000", Line1: "1 Lê Lợi", Ward: "Bến Nghé", District: "Quận 1", Province: "TP.HCM"}

func params(lines []domain.Line, skus map[string]domain.Sku) domain.CheckoutParams {
	return domain.CheckoutParams{
		CheckoutID: "c1", BuyerID: "buyer", Lines: lines, Skus: skus, ShippingFee: 30_000,
		Address: addr, PaymentMethod: domain.PaymentCOD, Now: at, NewID: seqIDs(),
	}
}

func TestBuildOrdersSplitsByShopWithTotals(t *testing.T) {
	skus := map[string]domain.Sku{
		"s1": sku("s1", "shopB", "sellerB", 100_000),
		"s2": sku("s2", "shopA", "sellerA", 50_000),
		"s3": sku("s3", "shopB", "sellerB", 20_000),
	}
	orders, err := domain.BuildOrders(params([]domain.Line{
		{SkuID: "s3", Quantity: 1}, {SkuID: "s2", Quantity: 3}, {SkuID: "s1", Quantity: 2},
	}, skus))
	require.NoError(t, err)
	require.Len(t, orders, 2)

	a, b := orders[0], orders[1]
	assert.Equal(t, []string{"order-1", "order-2"}, []string{a.ID, b.ID})
	assert.Equal(t, "shopA", a.ShopID)
	assert.Equal(t, "Shop shopA", a.ShopName)
	assert.Equal(t, "sellerA", a.SellerUserID)
	assert.Equal(t, int64(150_000), a.Subtotal)
	assert.Equal(t, int64(30_000), a.ShippingFee)
	assert.Equal(t, int64(180_000), a.Total)

	assert.Equal(t, "shopB", b.ShopID)
	assert.Equal(t, int64(220_000), b.Subtotal)
	assert.Equal(t, int64(250_000), b.Total)
	require.Len(t, b.Items, 2)
	assert.Equal(t, domain.Item{
		SkuID: "s1", ProductID: "p-s1", ProductName: "Product s1", VariantName: "Variant s1",
		ImageURL: ptr("https://img/s1"), UnitPrice: 100_000, Quantity: 2, LineTotal: 200_000,
	}, b.Items[0])
	assert.Equal(t, "s3", b.Items[1].SkuID)

	for _, o := range orders {
		assert.Equal(t, "c1", o.CheckoutID)
		assert.Equal(t, "buyer", o.BuyerID)
		assert.Equal(t, domain.StatusPending, o.Status)
		assert.Equal(t, addr, o.ShippingAddress)
		assert.Equal(t, domain.PaymentCOD, o.PaymentMethod)
		assert.Equal(t, at, o.CreatedAt)
		assert.Equal(t, at, o.UpdatedAt)
		assert.Zero(t, o.Version)
		assert.Nil(t, o.CancelReason)
		assert.Equal(t, []domain.HistoryEntry{{To: domain.StatusPending, ActorRole: domain.RoleSystem, At: at}}, o.History)
	}
}

func TestBuildOrdersPricesAtCurrentCatalogPriceAndSnapshotsEmptyImageAsNil(t *testing.T) {
	s := sku("s1", "shop", "seller", 75_000)
	s.ImageURL = ""
	orders, err := domain.BuildOrders(params([]domain.Line{{SkuID: "s1", Quantity: 4}}, map[string]domain.Sku{"s1": s}))
	require.NoError(t, err)
	require.Len(t, orders, 1)
	assert.Equal(t, int64(75_000), orders[0].Items[0].UnitPrice)
	assert.Equal(t, int64(300_000), orders[0].Items[0].LineTotal)
	assert.Nil(t, orders[0].Items[0].ImageURL)
}

func TestBuildOrdersReportsEveryProblemAndBuildsNothing(t *testing.T) {
	off := sku("off", "shopA", "sellerA", 1_000)
	off.Available = false
	skus := map[string]domain.Sku{
		"ok":   sku("ok", "shopA", "sellerA", 1_000),
		"off":  off,
		"mine": sku("mine", "shopB", "buyer", 1_000), // the buyer owns shopB
	}
	orders, err := domain.BuildOrders(params([]domain.Line{
		{SkuID: "ok", Quantity: 1}, {SkuID: "off", Quantity: 1}, {SkuID: "gone", Quantity: 1}, {SkuID: "mine", Quantity: 1},
	}, skus))
	var lp *domain.LineProblemsError
	require.ErrorAs(t, err, &lp)
	assert.Nil(t, orders)
	assert.Equal(t, []domain.LineProblem{
		{SkuID: "gone", Reason: domain.ProblemNotFound},
		{SkuID: "mine", Reason: domain.ProblemOwnShop},
		{SkuID: "off", Reason: domain.ProblemUnavailable},
	}, lp.Problems)
	assert.Equal(t, "items cannot be purchased: gone: NOT_FOUND, mine: OWN_SHOP, off: UNAVAILABLE", err.Error())
}

func TestStockLines(t *testing.T) {
	o := domain.Order{Items: []domain.Item{{SkuID: "a", Quantity: 2}, {SkuID: "b", Quantity: 5}}}
	assert.Equal(t, []domain.Line{{SkuID: "a", Quantity: 2}, {SkuID: "b", Quantity: 5}}, o.StockLines())
}
