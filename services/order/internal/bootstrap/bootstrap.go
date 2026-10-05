// Package bootstrap wires order-svc's adapters, use cases, HTTP API and
// background workers. cmd/server and the end-to-end tests share it.
package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"

	orderv1 "github.com/ngoxuanchien/e-commerce/gen/order/v1"
	"github.com/ngoxuanchien/e-commerce/pkg/kafkax"
	"github.com/ngoxuanchien/e-commerce/pkg/outbox"
	"github.com/ngoxuanchien/e-commerce/pkg/server"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/catalog"
	grpcadapter "github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/grpc"
	httpadapter "github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/http"
	kafkaadapter "github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/kafka"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/payment"
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

	// OnlineMethods are the accepted online payment methods
	// (ONLINE_PAYMENT_METHODS); an empty list means COD only.
	OnlineMethods []string
	// CommissionRateBps is the platform commission in basis points, snapshotted
	// on online orders at checkout (COMMISSION_RATE_BPS).
	CommissionRateBps int32
	// PaymentTimeout is how long an online order may stay AWAITING_PAYMENT
	// before the sweeper cancels it (PAYMENT_TIMEOUT).
	PaymentTimeout time.Duration
	// AutoCompleteAfter is how long a DELIVERED order stays open before the
	// system completes it; consumed by the sweep tick.
	AutoCompleteAfter time.Duration
	// PaymentCallTimeout is the per-attempt timeout of the calls to
	// payment-svc (0 = payment.DefaultTimeout).
	PaymentCallTimeout time.Duration
}

// claimMargin covers the time between claiming an idempotency key and Tx A
// inserting the orders (cart read, GetSkus with retries): orders become
// sweepable that much later than the claim.
const claimMargin = time.Minute

// claimTimeout is when a checkout's idempotency key may be reclaimed: by then
// the sweeper has cancelled whatever the dead run left PENDING.
func (c Config) claimTimeout() time.Duration {
	return c.PendingTimeout + c.SweeperInterval + claimMargin
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
	PaymentConn grpc.ClientConnInterface
	Producer    Producer
	// KafkaBrokers are the bootstrap brokers of the payment.events consumer
	// group; an empty list leaves the consumer out, for a deployment that
	// runs the workers elsewhere.
	KafkaBrokers []string
	Logger       *slog.Logger     // nil = slog.Default()
	Now          func() time.Time // nil = UTC wall clock
}

// App is the wired service.
type App struct {
	API      http.Handler // /api routes; wrap with authctx.Middleware
	Relay    *outbox.Relay
	Sweeper  *app.Sweeper
	Consumer *kafkax.Consumer // nil when Deps.KafkaBrokers is empty
	checks   map[string]server.Check
	// queries serves OrderInternal; logger is the wired logger.
	queries *app.CheckoutQuery
	logger  *slog.Logger
}

// New wires the service. It fails only when the payment.events consumer cannot
// join its group, which is a deployment error worth failing fast on.
func New(cfg Config, d Deps) (*App, error) {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }
	}
	store := postgres.NewStore(d.Pool)
	carts := redisadapter.NewCartStore(d.Redis)
	cat := catalog.New(d.CatalogConn, cfg.Catalog)
	pay := payment.New(d.PaymentConn, cfg.PaymentCallTimeout)

	var consumer *kafkax.Consumer
	if len(d.KafkaBrokers) > 0 {
		var err error
		consumer, err = kafkaadapter.NewConsumer(d.KafkaBrokers,
			kafkaadapter.NewHandler(app.NewPaymentEventHandler(store, d.Now, d.Logger), d.Logger), d.Logger)
		if err != nil {
			return nil, fmt.Errorf("payment events consumer: %w", err)
		}
	}

	return &App{
		API: httpadapter.NewRouter(httpadapter.Services{
			Cart: app.NewCartService(carts, cat, cfg.ShippingFeeFlat, d.Logger),
			Checkout: app.NewCheckoutService(store, carts, cat, app.CheckoutConfig{
				ShippingFee: cfg.ShippingFeeFlat, ClaimTimeout: cfg.claimTimeout(),
				OnlineMethods: cfg.OnlineMethods, CommissionRateBps: cfg.CommissionRateBps,
				PaymentTimeout: cfg.PaymentTimeout, Deadline: cfg.PendingTimeout,
				Now: d.Now, Logger: d.Logger,
			}),
			Orders: app.NewOrderService(store, d.Now),
		}),
		Relay: outbox.NewRelay(d.Pool, d.Producer, outbox.RelayConfig{Interval: cfg.RelayInterval, Logger: d.Logger}),
		Sweeper: app.NewSweeper(store, cat, pay, app.SweeperConfig{
			Interval: cfg.SweeperInterval, PendingTimeout: cfg.PendingTimeout,
			AutoCompleteAfter: cfg.AutoCompleteAfter, Now: d.Now, Logger: d.Logger,
		}),
		Consumer: consumer,
		logger:   d.Logger,
		queries:  app.NewCheckoutQuery(store),
		checks: map[string]server.Check{
			"postgres": d.Pool.Ping,
			"redis":    func(ctx context.Context) error { return d.Redis.Ping(ctx).Err() },
			"kafka":    d.Producer.Ping,
		},
	}, nil
}

// ReadinessChecks are the /readyz dependencies: Postgres, Redis, Kafka.
func (a *App) ReadinessChecks() map[string]server.Check { return a.checks }

// RegisterGRPC registers order-svc's internal gRPC API (OrderInternal, for
// payment-svc) on s. Call it on the server grpcx.NewServer returns, next to the
// health service it already registered.
func (a *App) RegisterGRPC(s grpc.ServiceRegistrar) {
	orderv1.RegisterOrderInternalServer(s, grpcadapter.NewServer(a.queries, a.logger))
}

// RunWorkers runs the outbox relay, the sweeper and the payment.events consumer
// until ctx is cancelled. A deployment without KafkaBrokers has no consumer.
func (a *App) RunWorkers(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = a.Relay.Run(ctx) }()
	go func() { defer wg.Done(); _ = a.Sweeper.Run(ctx) }()
	if a.Consumer != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer a.Consumer.Close()
			_ = a.Consumer.Run(ctx)
		}()
	}
	wg.Wait()
}
