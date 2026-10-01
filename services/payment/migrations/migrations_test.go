package migrations_test

import (
	"context"
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/idemkey"
	"github.com/ngoxuanchien/e-commerce/pkg/inbox"
	"github.com/ngoxuanchien/e-commerce/pkg/outbox"
	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
	"github.com/ngoxuanchien/e-commerce/services/payment/migrations"
)

// reviewFixIndexes are the lookup indexes 000004 adds: the provider-hint refund lookup and the
// age scans over provider_events and processed_events.
var reviewFixIndexes = []string{"refunds_provider_ref_idx", "provider_events_received_idx", "processed_events_processed_at_idx"}

func TestSharedSchemasCopiedVerbatim(t *testing.T) {
	b, err := fs.ReadFile(migrations.FS, "000002_payment_schema.up.sql")
	require.NoError(t, err)
	for name, sql := range map[string]string{"inbox": inbox.SchemaSQL, "idemkey": idemkey.SchemaSQL, "outbox": outbox.SchemaSQL} {
		assert.True(t, strings.Contains(string(b), sql), "copy pkg/%s/schema.sql verbatim", name)
	}
}

func TestMigrationsApplyCleanly(t *testing.T) {
	dsn := testutil.Postgres(t)
	require.NoError(t, pgxx.Migrate(dsn, migrations.FS))
	require.NoError(t, pgxx.Migrate(dsn, migrations.FS), "re-running is a no-op")

	ctx := context.Background()
	pool, err := pgxx.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()
	for _, table := range []string{"payments", "payment_allocations", "order_cancellations", "refunds",
		"provider_events", "fake_sessions", "processed_events", "idempotency_keys", "outbox"} {
		var exists bool
		require.NoError(t, pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists))
		assert.True(t, exists, table)
	}
	for _, index := range reviewFixIndexes {
		var exists bool
		require.NoError(t, pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, index).Scan(&exists))
		assert.True(t, exists, index)
	}
	_, err = pool.Exec(ctx, `INSERT INTO payments (id, checkout_id, buyer_id, method, provider, status, amount, expires_at, created_at, updated_at)
		VALUES (gen_random_uuid(), gen_random_uuid(), gen_random_uuid(), 'VNPAY', 'vnpay', 'LOST', 1, now(), now(), now())`)
	assert.Error(t, err, "status is constrained")
	_, err = pool.Exec(ctx, `INSERT INTO payments (id, checkout_id, buyer_id, method, provider, status, amount, expires_at, created_at, updated_at)
		VALUES (gen_random_uuid(), gen_random_uuid(), gen_random_uuid(), 'VNPAY', 'vnpay', 'PENDING', 0, now(), now(), now())`)
	assert.Error(t, err, "amount must be positive")
}

func TestDownRemovesEveryTable(t *testing.T) {
	dsn := testutil.Postgres(t)
	require.NoError(t, pgxx.Migrate(dsn, migrations.FS))
	ctx := context.Background()
	pool, err := pgxx.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()
	down, err := fs.ReadFile(migrations.FS, "000004_review_fixes.down.sql")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, string(down))
	require.NoError(t, err)
	for _, index := range reviewFixIndexes {
		var exists bool
		require.NoError(t, pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, index).Scan(&exists))
		assert.False(t, exists, "000004 down drops %s", index)
	}
	var hasLease bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_name = 'payments' AND column_name = 'reconcile_after')`).Scan(&hasLease))
	assert.False(t, hasLease, "000004 down drops payments.reconcile_after")
	for _, name := range []string{"000003_fake_refunds.down.sql", "000002_payment_schema.down.sql"} {
		down, err := fs.ReadFile(migrations.FS, name)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, string(down))
		require.NoError(t, err)
	}
	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name <> 'schema_migrations'`).Scan(&n))
	assert.Zero(t, n)
}
