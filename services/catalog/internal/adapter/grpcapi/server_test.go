package grpcapi_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	catalogv1 "github.com/ngoxuanchien/e-commerce/gen/catalog/v1"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/adapter/grpcapi"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/adapter/grpcapi/grpctest"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/adapter/postgres"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/testsupport"
)

const assets = "http://assets.test/product-images"

func newCatalog(t *testing.T) (catalogv1.CatalogInternalClient, *pgxpool.Pool) {
	t.Helper()
	pool := testsupport.DB(t)
	inv := app.NewInventoryService(postgres.New(pool))
	conn := grpctest.Serve(t, func(s *grpc.Server) {
		catalogv1.RegisterCatalogInternalServer(s, grpcapi.NewCatalogServer(inv, assets))
	})
	return catalogv1.NewCatalogInternalClient(conn), pool
}

func reserve(ctx context.Context, c catalogv1.CatalogInternalClient, order uuid.UUID, items ...*catalogv1.StockItem) error {
	_, err := c.ReserveStock(ctx, &catalogv1.ReserveStockRequest{OrderId: order.String(), Items: items})
	return err
}

func item(sku uuid.UUID, q int32) *catalogv1.StockItem {
	return &catalogv1.StockItem{SkuId: sku.String(), Quantity: q}
}

func reservedRows(t *testing.T, pool *pgxpool.Pool, status string) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT count(*) FROM stock_reservations WHERE status = $1`, status).Scan(&n))
	return n
}

func TestReserveStockConcurrentOrdersNeverOversell(t *testing.T) {
	client, pool := newCatalog(t)
	f := testsupport.SeedSku(t, pool, 10)

	var ok, short atomic.Int32
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := reserve(context.Background(), client, uuid.Must(uuid.NewV7()), item(f.SkuID, 1))
			switch status.Code(err) {
			case codes.OK:
				ok.Add(1)
			case codes.FailedPrecondition:
				short.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(10), ok.Load())
	assert.Equal(t, int32(40), short.Load())
	assert.Equal(t, int32(0), testsupport.Stock(t, pool, f.SkuID))
	assert.Equal(t, 10, reservedRows(t, pool, "reserved"))
}

func TestReserveStockSameOrderConcurrently(t *testing.T) {
	client, pool := newCatalog(t)
	f := testsupport.SeedSku(t, pool, 10)
	order := uuid.Must(uuid.NewV7())

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			assert.NoError(t, reserve(context.Background(), client, order, item(f.SkuID, 3)))
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(7), testsupport.Stock(t, pool, f.SkuID), "retries of one order reserve once")
	assert.Equal(t, 1, reservedRows(t, pool, "reserved"))
}

func TestReserveStockTwiceDecrementsOnce(t *testing.T) {
	client, pool := newCatalog(t)
	ctx := context.Background()
	f := testsupport.SeedSku(t, pool, 10)
	order := uuid.Must(uuid.NewV7())

	require.NoError(t, reserve(ctx, client, order, item(f.SkuID, 4)))
	require.NoError(t, reserve(ctx, client, order, item(f.SkuID, 4)))
	assert.Equal(t, int32(6), testsupport.Stock(t, pool, f.SkuID))
}

func TestReleaseStockTwiceIncrementsOnce(t *testing.T) {
	client, pool := newCatalog(t)
	ctx := context.Background()
	f := testsupport.SeedSku(t, pool, 10)
	order := uuid.Must(uuid.NewV7())
	require.NoError(t, reserve(ctx, client, order, item(f.SkuID, 4)))

	for range 2 {
		_, err := client.ReleaseStock(ctx, &catalogv1.ReleaseStockRequest{OrderId: order.String()})
		require.NoError(t, err)
	}
	assert.Equal(t, int32(10), testsupport.Stock(t, pool, f.SkuID))
	assert.Equal(t, 1, reservedRows(t, pool, "released"))

	_, err := client.ReleaseStock(ctx, &catalogv1.ReleaseStockRequest{OrderId: uuid.NewString()})
	require.NoError(t, err, "nothing reserved → OK")
}

func TestConcurrentReserveAndReleaseStayConsistent(t *testing.T) {
	client, pool := newCatalog(t)
	a := testsupport.SeedSku(t, pool, 20)
	b := testsupport.SeedSku(t, pool, 20)

	var wg sync.WaitGroup
	var kept atomic.Int32
	for i := range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			order := uuid.Must(uuid.NewV7())
			// Alternate item order so lock ordering is exercised both ways.
			items := []*catalogv1.StockItem{item(a.SkuID, 1), item(b.SkuID, 1)}
			if i%2 == 1 {
				items[0], items[1] = items[1], items[0]
			}
			err := reserve(ctx, client, order, items...)
			if status.Code(err) == codes.FailedPrecondition {
				return
			}
			if !assert.NoError(t, err) {
				return
			}
			if i%3 == 0 {
				kept.Add(1)
				return
			}
			_, err = client.ReleaseStock(ctx, &catalogv1.ReleaseStockRequest{OrderId: order.String()})
			assert.NoError(t, err)
		}()
	}
	wg.Wait()

	held := int32(reservedRows(t, pool, "reserved") / 2)
	assert.Equal(t, kept.Load(), held)
	assert.Equal(t, 20-held, testsupport.Stock(t, pool, a.SkuID))
	assert.Equal(t, 20-held, testsupport.Stock(t, pool, b.SkuID))
}

func TestReserveStockAllOrNothingWithShortageDetails(t *testing.T) {
	client, pool := newCatalog(t)
	a := testsupport.SeedSku(t, pool, 5)
	b := testsupport.SeedSku(t, pool, 1)

	err := reserve(context.Background(), client, uuid.Must(uuid.NewV7()), item(a.SkuID, 2), item(b.SkuID, 2))
	st := status.Convert(err)
	require.Equal(t, codes.FailedPrecondition, st.Code())
	require.Len(t, st.Details(), 1)
	detail, ok := st.Details()[0].(*catalogv1.InsufficientStock)
	require.True(t, ok, "detail is %T", st.Details()[0])
	require.Len(t, detail.GetShortages(), 1)
	assert.Equal(t, b.SkuID.String(), detail.GetShortages()[0].GetSkuId())
	assert.Equal(t, int32(2), detail.GetShortages()[0].GetRequested())
	assert.Equal(t, int32(1), detail.GetShortages()[0].GetAvailable())

	assert.Equal(t, int32(5), testsupport.Stock(t, pool, a.SkuID), "nothing reserved")
}

func TestReserveStockMergesDuplicateItems(t *testing.T) {
	client, pool := newCatalog(t)
	f := testsupport.SeedSku(t, pool, 10)
	require.NoError(t, reserve(context.Background(), client, uuid.Must(uuid.NewV7()), item(f.SkuID, 1), item(f.SkuID, 2)))
	assert.Equal(t, int32(7), testsupport.Stock(t, pool, f.SkuID))
	var q int32
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT quantity FROM stock_reservations`).Scan(&q))
	assert.Equal(t, int32(3), q)
}

func TestReserveStockInvalidArguments(t *testing.T) {
	client, pool := newCatalog(t)
	f := testsupport.SeedSku(t, pool, 10)
	ctx := context.Background()
	cases := map[string]*catalogv1.ReserveStockRequest{
		"bad order id":  {OrderId: "nope", Items: []*catalogv1.StockItem{item(f.SkuID, 1)}},
		"bad sku id":    {OrderId: uuid.NewString(), Items: []*catalogv1.StockItem{{SkuId: "x", Quantity: 1}}},
		"zero quantity": {OrderId: uuid.NewString(), Items: []*catalogv1.StockItem{item(f.SkuID, 0)}},
		"no items":      {OrderId: uuid.NewString()},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := client.ReserveStock(ctx, req)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
		})
	}
	_, err := client.ReleaseStock(ctx, &catalogv1.ReleaseStockRequest{OrderId: "nope"})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.Equal(t, int32(10), testsupport.Stock(t, pool, f.SkuID))
}

func TestGetSkus(t *testing.T) {
	client, pool := newCatalog(t)
	ctx := context.Background()
	f := testsupport.SeedSku(t, pool, 7)
	key := "shops/" + f.ShopID.String() + "/" + uuid.NewString() + ".jpg"
	_, err := pool.Exec(ctx, `INSERT INTO product_images (id, product_id, object_key, position) VALUES ($1, $2, $3, 0)`,
		uuid.Must(uuid.NewV7()), f.ProductID, key)
	require.NoError(t, err)

	resp, err := client.GetSkus(ctx, &catalogv1.GetSkusRequest{SkuIds: []string{f.SkuID.String(), f.SkuID.String(), uuid.NewString(), "garbage"}})
	require.NoError(t, err)
	require.Len(t, resp.GetSkus(), 1, "duplicates collapse; unknown and malformed ids are omitted")
	got := resp.GetSkus()[0]
	assert.Equal(t, f.SkuID.String(), got.GetSkuId())
	assert.Equal(t, f.ProductID.String(), got.GetProductId())
	assert.Equal(t, "Fixture product", got.GetProductName())
	assert.Equal(t, "Default", got.GetVariantName())
	assert.Equal(t, assets+"/"+key, got.GetImageUrl())
	assert.Equal(t, int64(100_000), got.GetPrice())
	assert.Equal(t, int32(7), got.GetStock())
	assert.True(t, got.GetAvailable())
	assert.Equal(t, f.ShopID.String(), got.GetShopId())
	assert.Equal(t, "Fixture shop", got.GetShopName())
	assert.Equal(t, f.OwnerID.String(), got.GetSellerUserId())
	assert.Equal(t, "fx-"+f.ShopID.String()[24:], got.GetShopSlug())

	tooMany := make([]string, 101)
	for i := range tooMany {
		tooMany[i] = uuid.NewString()
	}
	_, err = client.GetSkus(ctx, &catalogv1.GetSkusRequest{SkuIds: tooMany})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}
