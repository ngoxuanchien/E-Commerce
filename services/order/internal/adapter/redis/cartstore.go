// Package redisadapter stores carts in Redis as a hash per user:
// key cart:{user_id}, field sku_id, value quantity, no TTL (WS3 §2).
package redisadapter

import (
	"context"
	"fmt"
	"strconv"

	"github.com/redis/go-redis/v9"

	"github.com/ngoxuanchien/e-commerce/services/order/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

// CartStore implements app.CartStore.
type CartStore struct {
	rdb *redis.Client
}

var _ app.CartStore = (*CartStore)(nil)

// NewCartStore uses rdb.
func NewCartStore(rdb *redis.Client) *CartStore { return &CartStore{rdb: rdb} }

// setLine writes ARGV[1]=ARGV[2] into hash KEYS[1] unless it would add a new
// field beyond ARGV[3] fields. Atomic, so concurrent adds cannot exceed the cap.
var setLine = redis.NewScript(`
if redis.call('HEXISTS', KEYS[1], ARGV[1]) == 1 or redis.call('HLEN', KEYS[1]) < tonumber(ARGV[3]) then
  redis.call('HSET', KEYS[1], ARGV[1], ARGV[2])
  return 1
end
return 0
`)

func cartKey(userID string) string { return "cart:" + userID }

func (c *CartStore) Lines(ctx context.Context, userID string) (map[string]int32, error) {
	raw, err := c.rdb.HGetAll(ctx, cartKey(userID)).Result()
	if err != nil {
		return nil, fmt.Errorf("read cart: %w", err)
	}
	lines := make(map[string]int32, len(raw))
	for sku, v := range raw {
		q, err := strconv.ParseInt(v, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("cart of %s: bad quantity %q for sku %s", userID, v, sku)
		}
		lines[sku] = int32(q)
	}
	return lines, nil
}

func (c *CartStore) Set(ctx context.Context, userID, skuID string, quantity int32) error {
	ok, err := setLine.Run(ctx, c.rdb, []string{cartKey(userID)}, skuID, quantity, domain.MaxCartLines).Int()
	if err != nil {
		return fmt.Errorf("set cart line: %w", err)
	}
	if ok == 0 {
		return app.ErrCartFull
	}
	return nil
}

func (c *CartStore) Remove(ctx context.Context, userID string, skuIDs ...string) error {
	if len(skuIDs) == 0 {
		return nil
	}
	if err := c.rdb.HDel(ctx, cartKey(userID), skuIDs...).Err(); err != nil {
		return fmt.Errorf("remove cart lines: %w", err)
	}
	return nil
}
