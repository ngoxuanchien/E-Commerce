package redisadapter_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
	redisadapter "github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/redis"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

var ctx = context.Background()

func id() string { return uuid.Must(uuid.NewV7()).String() }

func TestCartStore(t *testing.T) {
	opts, err := redis.ParseURL(testutil.Redis(t))
	require.NoError(t, err)
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { _ = rdb.Close() })
	s := redisadapter.NewCartStore(rdb)

	t.Run("empty cart", func(t *testing.T) {
		lines, err := s.Lines(ctx, id())
		require.NoError(t, err)
		assert.NotNil(t, lines)
		assert.Empty(t, lines)
	})

	t.Run("set, update and read lines in the spec's key layout", func(t *testing.T) {
		user, a, b := id(), id(), id()
		require.NoError(t, s.Set(ctx, user, a, 2))
		require.NoError(t, s.Set(ctx, user, b, 1))
		require.NoError(t, s.Set(ctx, user, a, 5))

		lines, err := s.Lines(ctx, user)
		require.NoError(t, err)
		assert.Equal(t, map[string]int32{a: 5, b: 1}, lines)
		raw, err := rdb.HGet(ctx, "cart:"+user, a).Result()
		require.NoError(t, err)
		assert.Equal(t, "5", raw)
		ttl, err := rdb.TTL(ctx, "cart:"+user).Result()
		require.NoError(t, err)
		assert.Equal(t, int64(-1), int64(ttl), "no TTL")
	})

	t.Run("remove lines, unknown ones and none are fine", func(t *testing.T) {
		user, a, b := id(), id(), id()
		require.NoError(t, s.Set(ctx, user, a, 1))
		require.NoError(t, s.Set(ctx, user, b, 1))
		require.NoError(t, s.Remove(ctx, user, a, id()))
		require.NoError(t, s.Remove(ctx, user))
		lines, err := s.Lines(ctx, user)
		require.NoError(t, err)
		assert.Equal(t, map[string]int32{b: 1}, lines)
	})

	t.Run("carts are per user", func(t *testing.T) {
		u1, u2, sku := id(), id(), id()
		require.NoError(t, s.Set(ctx, u1, sku, 3))
		lines, err := s.Lines(ctx, u2)
		require.NoError(t, err)
		assert.Empty(t, lines)
	})

	t.Run("at most 50 distinct lines, updates always allowed", func(t *testing.T) {
		user := id()
		var skus []string
		for i := range domain.MaxCartLines {
			skus = append(skus, id())
			require.NoError(t, s.Set(ctx, user, skus[i], 1), fmt.Sprint(i))
		}
		assert.ErrorIs(t, s.Set(ctx, user, id(), 1), app.ErrCartFull)
		require.NoError(t, s.Set(ctx, user, skus[0], 9), "updating an existing line in a full cart")
		require.NoError(t, s.Remove(ctx, user, skus[1]))
		require.NoError(t, s.Set(ctx, user, id(), 1), "room again after a removal")
		lines, err := s.Lines(ctx, user)
		require.NoError(t, err)
		assert.Len(t, lines, domain.MaxCartLines)
	})
}
