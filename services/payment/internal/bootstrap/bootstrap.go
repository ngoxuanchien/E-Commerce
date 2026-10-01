// Package bootstrap wires payment-svc's adapters, use cases, HTTP API, gRPC service and
// background workers. cmd/server and the end-to-end tests share it.
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"

	paymentv1 "github.com/ngoxuanchien/e-commerce/gen/payment/v1"
	"github.com/ngoxuanchien/e-commerce/pkg/kafkax"
	"github.com/ngoxuanchien/e-commerce/pkg/outbox"
	"github.com/ngoxuanchien/e-commerce/pkg/server"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/fake"
	grpcadapter "github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/grpc"
	httpadapter "github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/http"
	kafkaadapter "github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/kafka"
	orderadapter "github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/order"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/postgres"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/stripe"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/vnpay"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/app"
)

// Provider names for Config.ProviderVNPay and Config.ProviderStripe.
const (
	ProviderFake   = "fake"
	ProviderVNPay  = "vnpay"
	ProviderStripe = "stripe"
)

// LockPoolMaxConns bounds the dedicated pool whose connections hold the Creator's per-checkout
// advisory locks, so concurrent creates can never exhaust the store pool.
const LockPoolMaxConns = 8

// Config holds the tunables.
type Config struct {
	Brokers       []string
	PublicBaseURL string

	ProviderVNPay  string // fake | vnpay
	ProviderStripe string // fake | stripe

	FakeWebhookURL, FakeWebhookSecret string
	FakeWebhookDelay                  time.Duration

	VNPayTmnCode, VNPayHashSecret, VNPayPayURL, VNPayAPIURL string
	StripeSecretKey, StripeWebhookSecret                    string
	StripeMinAmount                                         int64

	ReconcileInterval, ReconcileMinAge time.Duration
	RefundMaxAttempts                  int
	RefundInterval                     time.Duration // 0 = 10s
	RelayInterval                      time.Duration // 0 = outbox default (500ms)
	Order                              orderadapter.Config
}

const defaultRefundInterval = 10 * time.Second

// Producer publishes outbox messages and reports broker reachability (*kafkax.Producer).
type Producer interface {
	outbox.Publisher
	Ping(ctx context.Context) error
}

// Deps are the live connections.
type Deps struct {
	Pool *pgxpool.Pool
	// LockPool serves the Creator's idempotency keys and advisory-lock connections. Build it with
	// NewLockPool; nil falls back to Pool.
	LockPool  *pgxpool.Pool
	OrderConn grpc.ClientConnInterface
	Producer  Producer
	Logger    *slog.Logger     // nil = slog.Default()
	Now       func() time.Time // nil = UTC wall clock
}

// NewLockPool opens the small dedicated pool for the Creator.
func NewLockPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg.MaxConns = LockPoolMaxConns
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create lock pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// App is the wired service.
type App struct {
	API        http.Handler // /api routes; wrap with authctx.Middleware
	Relay      *outbox.Relay
	Consumer   *kafkax.Consumer
	Refunds    *app.RefundWorker
	Reconciler *app.Reconciler

	canceller      *app.Canceller
	fake           *fake.Provider // nil when no method uses FAKE
	refundInterval time.Duration
	logger         *slog.Logger
	checks         map[string]server.Check
}

// New wires the service. The Kafka consumer joins its group here, so a bad broker list fails fast.
func New(cfg Config, d Deps) (*App, error) {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }
	}
	if d.LockPool == nil {
		d.LockPool = d.Pool
	}
	if cfg.RefundInterval <= 0 {
		cfg.RefundInterval = defaultRefundInterval
	}
	store := postgres.NewStore(d.Pool)

	var fk *fake.Provider
	newFake := func() *fake.Provider {
		if fk == nil { // one instance when both methods use FAKE
			fk = fake.New(d.Pool, fake.Config{
				PublicBaseURL: cfg.PublicBaseURL, WebhookURL: cfg.FakeWebhookURL, WebhookSecret: cfg.FakeWebhookSecret,
				WebhookDelay: cfg.FakeWebhookDelay, Now: d.Now, Logger: d.Logger,
			})
		}
		return fk
	}

	var forVNPay, forStripe app.Provider
	var vn *vnpay.Provider
	var st *stripe.Provider
	switch cfg.ProviderVNPay {
	case ProviderFake:
		forVNPay = newFake()
	case ProviderVNPay:
		vn = vnpay.New(vnpay.Config{
			TmnCode: cfg.VNPayTmnCode, HashSecret: cfg.VNPayHashSecret, PayURL: cfg.VNPayPayURL, APIURL: cfg.VNPayAPIURL,
			PublicBaseURL: cfg.PublicBaseURL, Now: d.Now, Logger: d.Logger, Payments: store,
		})
		forVNPay = vn
	default:
		return nil, fmt.Errorf("unknown VNPAY provider %q", cfg.ProviderVNPay)
	}
	switch cfg.ProviderStripe {
	case ProviderFake:
		forStripe = newFake()
	case ProviderStripe:
		st = stripe.New(stripe.Config{
			SecretKey: cfg.StripeSecretKey, WebhookSecret: cfg.StripeWebhookSecret, PublicBaseURL: cfg.PublicBaseURL, Now: d.Now,
		})
		forStripe = st
	default:
		return nil, fmt.Errorf("unknown STRIPE provider %q", cfg.ProviderStripe)
	}
	providers := app.NewProviders(forVNPay, forStripe)

	observer := app.NewObserver(store, d.Now, d.Logger)
	creator := app.NewCreator(store, d.LockPool, orderadapter.New(d.OrderConn, cfg.Order), providers, observer,
		app.CreateConfig{PublicBaseURL: cfg.PublicBaseURL, StripeMinAmount: cfg.StripeMinAmount}, d.Now)
	refunds := app.NewRefunds(store, d.Now)
	worker := app.NewRefundWorker(store, providers, cfg.RefundMaxAttempts, d.Now, d.Logger)
	reconciler := app.NewReconciler(store, providers, observer,
		app.ReconcileConfig{Interval: cfg.ReconcileInterval, MinAge: cfg.ReconcileMinAge}, d.Now, d.Logger)
	canceller := app.NewCanceller(store, providers, observer, d.Now)

	var routes []httpadapter.Routes
	if fk != nil {
		routes = append(routes, httpadapter.NewFakeCallbacks(httpadapter.FakeDeps{
			Provider: fk, Store: store, Observer: observer, WebhookSecret: cfg.FakeWebhookSecret, Now: d.Now, Logger: d.Logger,
		}))
	}
	if vn != nil {
		routes = append(routes, httpadapter.NewVNPayCallbacks(vnpay.NewCallbacks(vn, store, observer, d.Logger)))
	}
	if st != nil {
		cb := stripe.NewCallbacks(st, store, observer, d.Logger)
		cb.RefundHint = stripe.RefundHintFor(worker)
		routes = append(routes, httpadapter.NewStripeCallbacks(cb))
	}

	consumer, err := kafkaadapter.NewConsumer(cfg.Brokers, kafkaadapter.NewHandler(refunds, d.Logger), d.Logger)
	if err != nil {
		return nil, err
	}

	return &App{
		API:            httpadapter.NewRouter(creator, routes...),
		Relay:          outbox.NewRelay(d.Pool, d.Producer, outbox.RelayConfig{Interval: cfg.RelayInterval, Logger: d.Logger}),
		Consumer:       consumer,
		Refunds:        worker,
		Reconciler:     reconciler,
		canceller:      canceller,
		fake:           fk,
		refundInterval: cfg.RefundInterval,
		logger:         d.Logger,
		checks:         map[string]server.Check{"postgres": d.Pool.Ping, "kafka": d.Producer.Ping},
	}, nil
}

// ReadinessChecks are the /readyz dependencies: Postgres and Kafka. Order-svc and the payment
// providers are deliberately not part of readiness.
func (a *App) ReadinessChecks() map[string]server.Check { return a.checks }

// RegisterGRPC registers PaymentInternal on srv, next to the health service.
func (a *App) RegisterGRPC(srv *grpc.Server) {
	paymentv1.RegisterPaymentInternalServer(srv, grpcadapter.NewServer(a.canceller, a.logger))
}

// RunWorkers runs the outbox relay, the order.events consumer, the refund worker and the
// reconciler until ctx is cancelled, then waits for them and for in-flight FAKE webhook deliveries,
// and closes the consumer.
func (a *App) RunWorkers(ctx context.Context) {
	var wg sync.WaitGroup
	run := func(name string, f func(context.Context) error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := f(ctx); err != nil && !errors.Is(err, context.Canceled) && ctx.Err() == nil {
				a.logger.Error("worker stopped", "worker", name, "error", err)
			}
		}()
	}
	run("outbox relay", a.Relay.Run)
	run("order events consumer", a.Consumer.Run)
	run("refund worker", func(ctx context.Context) error { return a.Refunds.Run(ctx, a.refundInterval) })
	run("reconciler", a.Reconciler.Run)
	wg.Wait()
	a.Consumer.Close()
	if a.fake != nil {
		a.fake.Wait()
	}
}
