package app_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/catalog"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/catalog/catalogfake"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/postgres"
	redisadapter "github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/redis"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
	"github.com/ngoxuanchien/e-commerce/services/order/migrations"
)

var ctx = context.Background()

var testAddress = domain.Address{FullName: "Nguyễn Văn A", Phone: "0900000000", Line1: "1 Lê Lợi", Ward: "Bến Nghé", District: "Quận 1", Province: "TP.HCM"}

func uid() string { return uuid.Must(uuid.NewV7()).String() }

// clock is a controllable app clock (UTC, µs precision like Postgres).
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Now().UTC().Truncate(time.Microsecond)} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type envOpts struct{ postgres, redis bool }

// env is the real adapters wired to throwaway containers and the catalog fake.
type env struct {
	store   *postgres.Store
	pool    *pgxpool.Pool
	rdb     *redis.Client
	carts   *redisadapter.CartStore
	fake    *catalogfake.Fake
	catalog *catalog.Client
	clock   *clock
}

func newEnv(t *testing.T, o envOpts) *env {
	e := &env{fake: catalogfake.New(), clock: newClock()}
	e.catalog = catalog.New(e.fake.Dial(t), catalog.Config{Timeout: 300 * time.Millisecond, Retries: 2, Backoff: 5 * time.Millisecond})
	if o.postgres {
		dsn := testutil.Postgres(t)
		require.NoError(t, pgxx.Migrate(dsn, migrations.FS))
		pool, err := pgxx.NewPool(ctx, dsn)
		require.NoError(t, err)
		t.Cleanup(pool.Close)
		e.pool, e.store = pool, postgres.NewStore(pool)
	}
	if o.redis {
		opts, err := redis.ParseURL(testutil.Redis(t))
		require.NoError(t, err)
		e.rdb = redis.NewClient(opts)
		t.Cleanup(func() { _ = e.rdb.Close() })
		e.carts = redisadapter.NewCartStore(e.rdb)
	}
	return e
}

func (e *env) cartSet(t *testing.T, user, skuID string, q int32) {
	t.Helper()
	require.NoError(t, e.carts.Set(ctx, user, skuID, q))
}

// events returns the outbox events of orderID in write order.
func (e *env) events(t *testing.T, orderID string) []event.Envelope {
	t.Helper()
	rows, err := e.pool.Query(ctx, `SELECT payload FROM outbox WHERE msg_key = $1 ORDER BY created_at, id`, orderID)
	require.NoError(t, err)
	defer rows.Close()
	var out []event.Envelope
	for rows.Next() {
		var b []byte
		require.NoError(t, rows.Scan(&b))
		env, err := event.Decode(b)
		require.NoError(t, err)
		out = append(out, env)
	}
	require.NoError(t, rows.Err())
	return out
}

func types(evs []event.Envelope) []event.Type {
	out := make([]event.Type, len(evs))
	for i, e := range evs {
		out[i] = e.EventType
	}
	return out
}

// seedOrder stores a one-item order of buyer at shop and walks it along the
// happy path until it reaches status (no outbox events are written).
func (e *env) seedOrder(t *testing.T, buyer string, shop catalogfake.Shop, status domain.Status) domain.Order {
	t.Helper()
	skuID := uid()
	orders, err := domain.BuildOrders(domain.CheckoutParams{
		CheckoutID: uid(), BuyerID: buyer, Lines: []domain.Line{{SkuID: skuID, Quantity: 1}},
		Skus: map[string]domain.Sku{skuID: {
			ID: skuID, ProductID: uid(), ProductName: "Áo", VariantName: "M", Price: 10_000, Stock: 5, Available: true,
			ShopID: shop.ID, ShopName: shop.Name, ShopSlug: shop.Slug, SellerUserID: shop.SellerUserID,
		}},
		ShippingFee: 30_000, Address: testAddress, PaymentMethod: domain.PaymentCOD, Now: e.clock.Now(), NewID: uid,
	})
	require.NoError(t, err)
	o := orders[0]
	require.NoError(t, e.store.InTx(ctx, func(tx app.Tx) error { return tx.InsertOrder(ctx, o) }))

	path := []struct {
		action domain.Action
		actor  domain.Actor
	}{
		{domain.ActionSubmit, domain.SystemActor()},
		{domain.ActionConfirm, domain.Actor{ID: shop.SellerUserID, Role: domain.RoleSeller}},
		{domain.ActionShip, domain.Actor{ID: shop.SellerUserID, Role: domain.RoleSeller}},
		{domain.ActionDeliver, domain.Actor{ID: shop.SellerUserID, Role: domain.RoleSeller}},
		{domain.ActionComplete, domain.Actor{ID: buyer, Role: domain.RoleBuyer}},
	}
	for _, step := range path {
		if o.Status == status {
			break
		}
		ch, err := domain.Transition(o, step.action, step.actor)
		require.NoError(t, err)
		prev := o.Version
		o.Apply(ch, "", e.clock.Now())
		require.NoError(t, e.store.InTx(ctx, func(tx app.Tx) error { return tx.SaveTransition(ctx, o, prev) }))
		o.Version = prev + 1
	}
	require.Equal(t, status, o.Status, "seedOrder only walks the happy path")
	return o
}
