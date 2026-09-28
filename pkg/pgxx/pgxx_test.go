package pgxx_test

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
)

var migrations = fstest.MapFS{
	"000001_items.up.sql":   {Data: []byte(`CREATE TABLE items (id int PRIMARY KEY, name text NOT NULL);`)},
	"000001_items.down.sql": {Data: []byte(`DROP TABLE items;`)},
}

func TestMigrateAndTransactions(t *testing.T) {
	dsn := testutil.Postgres(t)
	ctx := context.Background()

	require.NoError(t, pgxx.Migrate(dsn, migrations))
	require.NoError(t, pgxx.Migrate(dsn, migrations), "second run is a no-op")

	pool, err := pgxx.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	count := func() int {
		var n int
		require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM items`).Scan(&n))
		return n
	}

	t.Run("commit", func(t *testing.T) {
		err := pgxx.WithTx(ctx, pool, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO items VALUES (1, 'a')`)
			return err
		})
		require.NoError(t, err)
		assert.Equal(t, 1, count())
	})

	t.Run("rollback on error", func(t *testing.T) {
		boom := errors.New("boom")
		err := pgxx.WithTx(ctx, pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `INSERT INTO items VALUES (2, 'b')`); err != nil {
				return err
			}
			return boom
		})
		assert.ErrorIs(t, err, boom)
		assert.Equal(t, 1, count())
	})

	t.Run("rollback on panic", func(t *testing.T) {
		assert.Panics(t, func() {
			_ = pgxx.WithTx(ctx, pool, func(tx pgx.Tx) error {
				_, _ = tx.Exec(ctx, `INSERT INTO items VALUES (3, 'c')`)
				panic("boom")
			})
		})
		assert.Equal(t, 1, count())
	})
}

func TestNewPoolFailsFastOnBadURL(t *testing.T) {
	_, err := pgxx.NewPool(context.Background(), "postgres://nobody@127.0.0.1:1/none?connect_timeout=1")
	assert.Error(t, err)
}
