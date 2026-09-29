package testsupport_test

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/testsupport"
)

func TestMain(m *testing.M) { os.Exit(testsupport.RunWithPostgres(m)) }

func TestMigrationsSeedTwoLevelCategoryTree(t *testing.T) {
	pool := testsupport.DB(t)
	ctx := context.Background()

	var top, children, deeper int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE parent_id IS NULL), count(*) FILTER (WHERE parent_id IS NOT NULL)
		FROM categories`).Scan(&top, &children))
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(*) FROM categories c JOIN categories p ON p.id = c.parent_id
		WHERE p.parent_id IS NOT NULL`).Scan(&deeper))

	assert.Equal(t, 10, top)
	assert.Equal(t, 30, children)
	assert.Zero(t, deeper, "tree must have exactly two levels")

	var slug string
	require.NoError(t, pool.QueryRow(ctx, `SELECT slug FROM categories WHERE id = $1`, testsupport.CategoryAoThunNam).Scan(&slug))
	assert.Equal(t, "ao-thun-nam", slug)
}

func TestVnUnaccentStripsVietnameseDiacritics(t *testing.T) {
	pool := testsupport.DB(t)
	var out string
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT vn_unaccent('Áo thun Đầm Ốp lưng')`).Scan(&out))
	assert.Equal(t, "Ao thun Dam Op lung", out)
}

func TestSearchVectorIsAccentAndCaseInsensitive(t *testing.T) {
	pool := testsupport.DB(t)
	ctx := context.Background()
	f := testsupport.SeedSku(t, pool, 1)
	_, err := pool.Exec(ctx, `UPDATE products SET name = 'Áo thun Đen cổ tròn' WHERE id = $1`, f.ProductID)
	require.NoError(t, err)

	var match bool
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT search_vector @@ websearch_to_tsquery('simple', vn_unaccent('AO THUN den'))
		FROM products WHERE id = $1`, f.ProductID).Scan(&match))
	assert.True(t, match)
}

func TestStockCannotGoNegative(t *testing.T) {
	pool := testsupport.DB(t)
	f := testsupport.SeedSku(t, pool, 1)
	_, err := pool.Exec(context.Background(), `UPDATE skus SET stock = stock - 2 WHERE id = $1`, f.SkuID)
	require.Error(t, err)
	assert.Equal(t, int32(1), testsupport.Stock(t, pool, f.SkuID))
}

func TestEachTestGetsItsOwnDatabase(t *testing.T) {
	a, b := testsupport.DB(t), testsupport.DB(t)
	testsupport.SeedSku(t, a, 1)
	var n int
	require.NoError(t, b.QueryRow(context.Background(), `SELECT count(*) FROM shops`).Scan(&n))
	assert.Zero(t, n)
}
