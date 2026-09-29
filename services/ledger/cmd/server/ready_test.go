package main

import (
	"context"
	"maps"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/kafkax"
	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
	"github.com/ngoxuanchien/e-commerce/services/ledger/migrations"
)

func TestReadinessChecksAgainstRealDependencies(t *testing.T) {
	ctx := context.Background()
	dsn := testutil.Postgres(t)
	require.NoError(t, pgxx.Migrate(dsn, migrations.FS), "baseline migration applies")
	pool, err := pgxx.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()
	producer, err := kafkax.NewProducer(testutil.Kafka(t))
	require.NoError(t, err)
	defer producer.Close()

	checks := readinessChecks(pool, producer)
	assert.ElementsMatch(t, []string{"postgres", "kafka"}, slices.Collect(maps.Keys(checks)))
	for name, check := range checks {
		assert.NoError(t, check(ctx), name)
	}
}
