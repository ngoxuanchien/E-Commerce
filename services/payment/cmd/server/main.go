// Command server runs payment-svc: the payment REST API and provider callbacks, the
// PaymentInternal gRPC service and the background workers.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ngoxuanchien/e-commerce/pkg/config"
	"github.com/ngoxuanchien/e-commerce/pkg/grpcx"
	"github.com/ngoxuanchien/e-commerce/pkg/kafkax"
	"github.com/ngoxuanchien/e-commerce/pkg/logx"
	"github.com/ngoxuanchien/e-commerce/pkg/otelx"
	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/pkg/server"
	orderadapter "github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/order"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/bootstrap"
	"github.com/ngoxuanchien/e-commerce/services/payment/migrations"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(server.HealthcheckCommand(os.Getenv("HTTP_ADDR")))
	}
	if err := run(); err != nil {
		slog.Error("server exited", "error", err)
		os.Exit(1)
	}
}

// readinessChecks are /readyz's dependencies: Postgres and Kafka.
func readinessChecks(pool *pgxpool.Pool, producer *kafkax.Producer) map[string]server.Check {
	return map[string]server.Check{"postgres": pool.Ping, "kafka": producer.Ping}
}

// bootstrapConfig maps the environment onto the wiring's tunables.
func bootstrapConfig(cfg Config) bootstrap.Config {
	return bootstrap.Config{
		Brokers: cfg.KafkaBrokers, PublicBaseURL: cfg.PublicBaseURL,
		ProviderVNPay: cfg.ProviderVNPay, ProviderStripe: cfg.ProviderStripe,
		FakeWebhookURL: cfg.FakeWebhookURL, FakeWebhookSecret: cfg.FakeWebhookSecret, FakeWebhookDelay: cfg.FakeWebhookDelay,
		VNPayTmnCode: cfg.VNPayTmnCode, VNPayHashSecret: cfg.VNPayHashSecret, VNPayPayURL: cfg.VNPayPayURL, VNPayAPIURL: cfg.VNPayAPIURL,
		StripeSecretKey: cfg.StripeSecretKey, StripeWebhookSecret: cfg.StripeWebhookSecret, StripeMinAmount: cfg.StripeMinAmount,
		ReconcileInterval: cfg.ReconcileInterval, ReconcileMinAge: cfg.ReconcileMinAge, RefundMaxAttempts: cfg.RefundMaxAttempts,
		Order: orderadapter.DefaultConfig(),
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load[Config]()
	if err != nil {
		return err
	}
	if err := cfg.validate(); err != nil {
		return err
	}
	logger := logx.New(os.Stdout, cfg.LogLevel).With("service", cfg.ServiceName)
	slog.SetDefault(logger)

	shutdownOtel, err := otelx.Setup(ctx, cfg.ServiceName, cfg.OTLPEndpoint)
	if err != nil {
		return err
	}
	defer func() { _ = shutdownOtel(context.Background()) }()

	if err := pgxx.Migrate(cfg.DatabaseURL, migrations.FS); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	pool, err := pgxx.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	lockPool, err := bootstrap.NewLockPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer lockPool.Close()

	producer, err := kafkax.NewProducer(cfg.KafkaBrokers)
	if err != nil {
		return err
	}
	defer producer.Close()

	orderConn, err := grpcx.Dial(cfg.OrderGRPCAddr)
	if err != nil {
		return fmt.Errorf("dial order: %w", err)
	}
	defer func() { _ = orderConn.Close() }()

	svc, err := bootstrap.New(bootstrapConfig(cfg), bootstrap.Deps{
		Pool: pool, LockPool: lockPool, OrderConn: orderConn, Producer: producer, Logger: logger,
	})
	if err != nil {
		return err
	}

	// An unexpected worker exit cancels rootCtx, which shuts the servers down and makes run return
	// the worker's error (exit code 1). Order: servers stop, then workers are awaited, then the
	// deferred pool/producer closes run.
	rootCtx, cancelRoot := context.WithCancel(ctx)
	defer cancelRoot()
	workersErr := make(chan error, 1)
	go func() { workersErr <- svc.RunWorkers(rootCtx, cancelRoot) }()

	grpcSrv, _ := grpcx.NewServer()
	svc.RegisterGRPC(grpcSrv)
	serveErr := server.Run(rootCtx, server.Config{
		HTTP: &http.Server{
			Addr:              cfg.HTTPAddr,
			Handler:           otelx.HTTPHandler(newHandler(logger, svc.ReadinessChecks(), svc.API), cfg.ServiceName),
			ReadHeaderTimeout: 10 * time.Second,
		},
		GRPC:            grpcSrv,
		GRPCAddr:        cfg.GRPCAddr,
		ShutdownTimeout: cfg.ShutdownTimeout,
		Logger:          logger,
	})
	cancelRoot()
	if err := <-workersErr; err != nil {
		return err
	}
	return serveErr
}
