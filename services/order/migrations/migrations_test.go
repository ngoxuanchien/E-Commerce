package migrations_test

import (
	"context"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/outbox"
	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
	"github.com/ngoxuanchien/e-commerce/services/order/migrations"
)

func TestOutboxMigrationMatchesSharedSchema(t *testing.T) {
	b, err := fs.ReadFile(migrations.FS, "000003_outbox.up.sql")
	require.NoError(t, err)
	assert.Equal(t, outbox.SchemaSQL, string(b), "copy pkg/outbox/schema.sql verbatim")
}

func TestMigrationsApplyCleanly(t *testing.T) {
	dsn := testutil.Postgres(t)
	require.NoError(t, pgxx.Migrate(dsn, migrations.FS))
	require.NoError(t, pgxx.Migrate(dsn, migrations.FS), "re-running is a no-op")

	ctx := context.Background()
	pool, err := pgxx.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()
	for _, table := range []string{"orders", "order_items", "order_status_history", "idempotency_keys", "outbox"} {
		var exists bool
		require.NoError(t, pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists))
		assert.True(t, exists, table)
	}
	_, err = pool.Exec(ctx, `INSERT INTO orders (id, checkout_id, buyer_id, shop_id, shop_name, seller_user_id, status,
		subtotal, shipping_fee, total, shipping_address, created_at, updated_at)
		VALUES (gen_random_uuid(), gen_random_uuid(), gen_random_uuid(), gen_random_uuid(), 's', gen_random_uuid(),
		'LOST', 0, 0, 0, '{}', now(), now())`)
	assert.Error(t, err, "status is constrained to the state machine's values")
}
