package migrations_test

import (
	"context"
	"fmt"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/inbox"
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

func TestInboxMigrationMatchesSharedSchema(t *testing.T) {
	b, err := fs.ReadFile(migrations.FS, "000006_processed_events.up.sql")
	require.NoError(t, err)
	assert.Equal(t, inbox.SchemaSQL, string(b), "copy pkg/inbox/schema.sql verbatim")
}

func TestMigrationsApplyCleanly(t *testing.T) {
	dsn := testutil.Postgres(t)
	require.NoError(t, pgxx.Migrate(dsn, migrations.FS))
	require.NoError(t, pgxx.Migrate(dsn, migrations.FS), "re-running is a no-op")

	ctx := context.Background()
	pool, err := pgxx.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()
	for _, table := range []string{"orders", "order_items", "order_status_history", "idempotency_keys", "outbox", "processed_events"} {
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

// P1 rows (inserted with the P1 column list) get the P2 defaults, and the P2
// status and payment method values are accepted.
func TestP2ColumnsDefaultForP1Rows(t *testing.T) {
	dsn := testutil.Postgres(t)
	require.NoError(t, pgxx.Migrate(dsn, migrations.FS))
	ctx := context.Background()
	pool, err := pgxx.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	const insertOrder = `INSERT INTO orders (id, checkout_id, buyer_id, shop_id, shop_name, seller_user_id, status,
		subtotal, shipping_fee, total, shipping_address, %s created_at, updated_at)
		VALUES ($1, gen_random_uuid(), gen_random_uuid(), gen_random_uuid(), 's', gen_random_uuid(),
		$2, 0, 0, 0, '{}', %s now(), now())`
	id := "01890000-0000-7000-8000-000000000002"
	_, err = pool.Exec(ctx, fmt.Sprintf(insertOrder, "", ""), id, "PENDING_CONFIRMATION")
	require.NoError(t, err)

	var ps, slug string
	var bps int32
	var amt int64
	var dl, da *time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT payment_status, shop_slug, commission_rate_bps, commission_amount, payment_deadline, delivered_at FROM orders WHERE id=$1`, id).Scan(&ps, &slug, &bps, &amt, &dl, &da))
	assert.Equal(t, "NONE", ps)
	assert.Equal(t, "", slug)
	assert.Zero(t, bps)
	assert.Zero(t, amt)
	assert.Nil(t, dl)
	assert.Nil(t, da)

	_, err = pool.Exec(ctx, fmt.Sprintf(insertOrder, "payment_method,", "'VNPAY',"), "01890000-0000-7000-8000-000000000003", "AWAITING_PAYMENT")
	assert.NoError(t, err, "AWAITING_PAYMENT with VNPAY is valid")
	_, err = pool.Exec(ctx, fmt.Sprintf(insertOrder, "payment_method,", "'CASH',"), "01890000-0000-7000-8000-000000000004", "PENDING")
	assert.Error(t, err, "payment_method is constrained to COD, VNPAY and STRIPE")
}

// The seller order list without ?status= must be served in index order, not
// by filtering orders_seller_idx (seller, status, ...) and sorting.
func TestSellerOrderListWithoutStatusUsesCreatedIndex(t *testing.T) {
	dsn := testutil.Postgres(t)
	require.NoError(t, pgxx.Migrate(dsn, migrations.FS))
	ctx := context.Background()
	pool, err := pgxx.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	conn, err := pool.Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()
	_, err = conn.Exec(ctx, `SET enable_seqscan = off`)
	require.NoError(t, err)
	var plan string
	rows, err := conn.Query(ctx, `EXPLAIN SELECT * FROM orders
		WHERE seller_user_id = '01890000-0000-7000-8000-000000000001'::uuid
		  AND (NULL::text IS NULL OR status = NULL::text)
		  AND (NULL::timestamptz IS NULL OR (created_at, id) < (NULL::timestamptz, NULL::uuid))
		ORDER BY created_at DESC, id DESC LIMIT 20`)
	require.NoError(t, err)
	for rows.Next() {
		var line string
		require.NoError(t, rows.Scan(&line))
		plan += line + "\n"
	}
	require.NoError(t, rows.Err())
	assert.Contains(t, plan, "orders_seller_created_idx", plan)
	assert.NotContains(t, plan, "Sort", plan)
}

// upTo is the migration set as it was at version v (the files numbered ≤ v).
func upTo(t *testing.T, v int) fs.FS {
	t.Helper()
	entries, err := fs.ReadDir(migrations.FS, ".")
	require.NoError(t, err)
	out := fstest.MapFS{}
	for _, e := range entries {
		var n int
		_, err := fmt.Sscanf(e.Name(), "%06d_", &n)
		require.NoError(t, err, e.Name())
		if n > v {
			continue
		}
		b, err := fs.ReadFile(migrations.FS, e.Name())
		require.NoError(t, err)
		out[e.Name()] = &fstest.MapFile{Data: b}
	}
	return out
}

// Orders already DELIVERED (or COMPLETED) before P2 get delivered_at from
// their latest DELIVERED history row, so auto-complete can pick them up.
func TestDeliveredAtIsBackfilledFromHistory(t *testing.T) {
	dsn := testutil.Postgres(t)
	require.NoError(t, pgxx.Migrate(dsn, upTo(t, 6)))
	ctx := context.Background()
	pool, err := pgxx.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	const (
		delivered  = "01890000-0000-7000-8000-000000000010"
		completed  = "01890000-0000-7000-8000-000000000011"
		shipping   = "01890000-0000-7000-8000-000000000012"
		alreadySet = "01890000-0000-7000-8000-000000000013"
	)
	first := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	latest := time.Date(2026, 5, 3, 9, 30, 0, 0, time.UTC)
	set := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	insertOrder := func(id, status string) {
		t.Helper()
		_, err := pool.Exec(ctx, `INSERT INTO orders (id, checkout_id, buyer_id, shop_id, shop_name, seller_user_id, status,
			subtotal, shipping_fee, total, shipping_address, created_at, updated_at)
			VALUES ($1, gen_random_uuid(), gen_random_uuid(), gen_random_uuid(), 's', gen_random_uuid(),
			$2, 0, 0, 0, '{}', now(), now())`, id, status)
		require.NoError(t, err)
	}
	insertHistory := func(orderID, to string, at time.Time) {
		t.Helper()
		_, err := pool.Exec(ctx, `INSERT INTO order_status_history (id, order_id, to_status, actor_role, created_at)
			VALUES (gen_random_uuid(), $1, $2, 'seller', $3)`, orderID, to, at)
		require.NoError(t, err)
	}
	insertOrder(delivered, "DELIVERED")
	insertHistory(delivered, "SHIPPING", first.Add(-time.Hour))
	insertHistory(delivered, "DELIVERED", first)
	insertHistory(delivered, "DELIVERED", latest) // the latest DELIVERED row wins
	insertOrder(completed, "COMPLETED")
	insertHistory(completed, "DELIVERED", first)
	insertHistory(completed, "COMPLETED", latest)
	insertOrder(shipping, "SHIPPING")
	insertHistory(shipping, "SHIPPING", first)

	// Written by P2 code between 000005 and 000007: left as it is.
	insertOrder(alreadySet, "DELIVERED")
	_, err = pool.Exec(ctx, `UPDATE orders SET delivered_at = $2 WHERE id = $1`, alreadySet, set)
	require.NoError(t, err)
	insertHistory(alreadySet, "DELIVERED", latest)

	require.NoError(t, pgxx.Migrate(dsn, migrations.FS))

	deliveredAt := func(id string) *time.Time {
		t.Helper()
		var at *time.Time
		require.NoError(t, pool.QueryRow(ctx, `SELECT delivered_at FROM orders WHERE id = $1`, id).Scan(&at))
		return at
	}
	require.NotNil(t, deliveredAt(delivered))
	assert.True(t, latest.Equal(*deliveredAt(delivered)), "latest DELIVERED history row: %v", deliveredAt(delivered))
	require.NotNil(t, deliveredAt(completed))
	assert.True(t, first.Equal(*deliveredAt(completed)), "COMPLETED orders keep an honest delivered_at")
	assert.Nil(t, deliveredAt(shipping), "never delivered")
	assert.True(t, set.Equal(*deliveredAt(alreadySet)))
}
