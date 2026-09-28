package testsupport

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Category ids seeded by migrations/000003_seed_categories.up.sql.
var (
	CategoryThoiTrangNam = uuid.MustParse("01a0e6ff-fbfb-7155-aa78-0de8daf1409d") // top level
	CategoryThoiTrangNu  = uuid.MustParse("01a0e6ff-fbfc-78f0-a941-27bbd89dc124") // top level
	CategoryAoThunNam    = uuid.MustParse("01a0e6ff-fc05-780a-a698-2b092498208a") // child of thoi-trang-nam
	CategoryVayDam       = uuid.MustParse("01a0e6ff-fc08-7250-978b-3d9a1282cddb") // child of thoi-trang-nu
)

// SkuFixture identifies the rows created by SeedSku.
type SkuFixture struct {
	ShopID, OwnerID, ProductID, SkuID uuid.UUID
}

// SeedSku inserts a shop, an active product and one active SKU priced 100,000
// with the given stock, bypassing the application layer.
func SeedSku(t testing.TB, pool *pgxpool.Pool, stock int32) SkuFixture {
	t.Helper()
	f := SkuFixture{ShopID: NewID(t), OwnerID: NewID(t), ProductID: NewID(t), SkuID: NewID(t)}
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	exec(`INSERT INTO shops (id, owner_user_id, name, slug) VALUES ($1, $2, 'Fixture shop', $3)`,
		f.ShopID, f.OwnerID, "fx-"+f.ShopID.String()[24:])
	exec(`INSERT INTO products (id, shop_id, category_id, name, status, min_price)
		VALUES ($1, $2, $3, 'Fixture product', 'active', 100000)`, f.ProductID, f.ShopID, CategoryAoThunNam)
	exec(`INSERT INTO skus (id, product_id, name, price, stock) VALUES ($1, $2, 'Default', 100000, $3)`,
		f.SkuID, f.ProductID, stock)
	return f
}

// Stock returns the on-hand stock of sku.
func Stock(t testing.TB, pool *pgxpool.Pool, sku uuid.UUID) int32 {
	t.Helper()
	var n int32
	if err := pool.QueryRow(context.Background(), `SELECT stock FROM skus WHERE id = $1`, sku).Scan(&n); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	return n
}

// NewID returns a UUIDv7.
func NewID(t testing.TB) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	return id
}
