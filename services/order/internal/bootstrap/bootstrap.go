// Package bootstrap wires order-svc's adapters, use cases, HTTP API and
// background workers. cmd/server and the end-to-end tests share it.
package bootstrap

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"

	"github.com/ngoxuanchien/e-commerce/pkg/outbox"
	"github.com/ngoxuanchien/e-commerce/pkg/server"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/catalog"
	httpadapter "github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/http"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/postgres"
	redisadapter "github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/redis"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/app"
)

// Config holds the tunables.
type Config struct {
	ShippingFeeFlat int64
	SweeperInterval time.Duration
	PendingTimeout  time.Duration
	Catalog         catalog.Config
	RelayInterval   time.Duration // 0 = outbox default (500ms)
}

// Producer publishes outbox messages and reports broker reachability
// (*kafkax.Producer).
type Producer interface {
	outbox.Publisher
	Ping(ctx context.Context) error
}

// Deps are the live connections.
type Deps struct {
	Pool        *pgxpool.Pool
	Redis       *redis.Client
	CatalogConn grpc.ClientConnInterface
	Producer    Producer
	Logger      *slog.Logger     // nil = slog.Default()
	Now         func() time.Time // nil = UTC wall clock
}

// App is the wired service.
type App struct {
	API     http.Handler // /api routes; wrap with authctx.Middleware
	Relay   *outbox.Relay
	Sweeper *app.Sweeper
	checks  map[string]server.Check
}

func New(cfg Config, d Deps) *App {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }
	}
	store := postgres.NewStore(d.Pool)
	carts := redisadapter.NewCartStore(d.Redis)
	cat := catalog.New(d.CatalogConn, cfg.Catalog)
	return &App{
		API: httpadapter.NewRouter(httpadapter.Services{
			Cart: app.NewCartService(carts, cat, cfg.ShippingFeeFlat, d.Logger),
			Checkout: app.NewCheckoutService(store, carts, cat, app.CheckoutConfig{
				ShippingFee: cfg.ShippingFeeFlat, ClaimTimeout: cfg.PendingTimeout + cfg.SweeperInterval,
				Now: d.Now, Logger: d.Logger,
			}),
			Orders: app.NewOrderService(store, d.Now),
		}),
		Relay: outbox.NewRelay(d.Pool, d.Producer, outbox.RelayConfig{Interval: cfg.RelayInterval, Logger: d.Logger}),
		Sweeper: app.NewSweeper(store, cat, app.SweeperConfig{
			Interval: cfg.SweeperInterval, PendingTimeout: cfg.PendingTimeout, Now: d.Now, Logger: d.Logger,
		}),
		checks: map[string]server.Check{
			"postgres": d.Pool.Ping,
			"redis":    func(ctx context.Context) error { return d.Redis.Ping(ctx).Err() },
			"kafka":    d.Producer.Ping,
		},
	}
}

// ReadinessChecks are the /readyz dependencies: Postgres, Redis, Kafka.
func (a *App) ReadinessChecks() map[string]server.Check { return a.checks }

// RunWorkers runs the outbox relay and the sweeper until ctx is cancelled.
func (a *App) RunWorkers(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = a.Relay.Run(ctx) }()
	go func() { defer wg.Done(); _ = a.Sweeper.Run(ctx) }()
	wg.Wait()
}
