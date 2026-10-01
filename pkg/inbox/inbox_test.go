package inbox_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/inbox"
	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
)

func newPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxx.NewPool(ctx, testutil.Postgres(t))
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	_, err = pool.Exec(ctx, inbox.SchemaSQL)
	require.NoError(t, err)
	return pool
}

func TestMarkProcessed(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	const id = "01890000-0000-7000-8000-000000000001"

	first, err := inbox.MarkProcessed(ctx, pool, "ledger-svc", id)
	require.NoError(t, err)
	assert.True(t, first)

	again, err := inbox.MarkProcessed(ctx, pool, "ledger-svc", id)
	require.NoError(t, err)
	assert.False(t, again, "a redelivered event is a duplicate")

	other, err := inbox.MarkProcessed(ctx, pool, "order-svc", id)
	require.NoError(t, err)
	assert.True(t, other, "duplicates are scoped per consumer")
}

func TestMarkProcessedRollsBackWithTheEffect(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	const id = "01890000-0000-7000-8000-000000000002"
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	first, err := inbox.MarkProcessed(ctx, tx, "payment-svc", id)
	require.NoError(t, err)
	require.True(t, first)
	require.NoError(t, tx.Rollback(ctx)) // the consumer's effect failed

	first, err = inbox.MarkProcessed(ctx, pool, "payment-svc", id)
	require.NoError(t, err)
	assert.True(t, first, "the redelivery is processed again")
}

func TestPurgeBefore(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	const (
		oldID    = "01890000-0000-7000-8000-000000000003"
		recentID = "01890000-0000-7000-8000-000000000004"
		consumer = "payment-svc"
	)

	for _, id := range []string{oldID, recentID} {
		first, err := inbox.MarkProcessed(ctx, pool, consumer, id)
		require.NoError(t, err)
		require.True(t, first)
	}
	_, err := pool.Exec(ctx, `UPDATE processed_events SET processed_at = now() - interval '40 days'
		WHERE consumer = $1 AND event_id = $2`, consumer, oldID)
	require.NoError(t, err)

	purged, err := inbox.PurgeBefore(ctx, pool, time.Now().Add(-30*24*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, int64(1), purged, "only the marker past the retention window goes")

	first, err := inbox.MarkProcessed(ctx, pool, consumer, oldID)
	require.NoError(t, err)
	assert.True(t, first, "a purged marker is processed again")

	again, err := inbox.MarkProcessed(ctx, pool, consumer, recentID)
	require.NoError(t, err)
	assert.False(t, again, "a marker inside the retention window stays")
}

func TestSchemaIsIdempotent(t *testing.T) {
	pool := newPool(t)
	_, err := pool.Exec(context.Background(), inbox.SchemaSQL)
	assert.NoError(t, err, "SchemaSQL can be applied twice")
}
