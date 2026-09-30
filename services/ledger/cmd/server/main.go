// Command server runs ledger-svc: the posting consumers, the seller wallet API, payouts and the trial-balance check.
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
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/bootstrap"
	"github.com/ngoxuanchien/e-commerce/services/ledger/migrations"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(server.HealthcheckCommand(os.Getenv("HTTP_ADDR")))
	}
	if len(os.Args) > 1 && os.Args[1] == "trial-balance" {
		os.Exit(trialBalance(context.Background(), os.Getenv("DATABASE_URL"), os.Stdout, os.Stderr))
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

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load[Config]()
	if err != nil {
		return err
	}
	if err := cfg.validate(); err != nil {
		return fmt.Errorf("config: %w", err)
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

	producer, err := kafkax.NewProducer(cfg.KafkaBrokers)
	if err != nil {
		return err
	}
	defer producer.Close()

	svc, err := bootstrap.New(bootstrap.Config{
		Brokers: cfg.KafkaBrokers, PayoutMinAmount: cfg.PayoutMinAmount,
		SettleInterval: cfg.PayoutSettleInterval, SettleDelay: cfg.PayoutSettleDelay,
		PendingInterval: cfg.PendingPostingInterval,
	}, bootstrap.Deps{Pool: pool, Producer: producer, Logger: logger})
	if err != nil {
		return err
	}

	// Workers stop (deferred last, so run first) before the connections close.
	workersCtx, stopWorkers := context.WithCancel(ctx)
	workersDone := make(chan struct{})
	go func() { defer close(workersDone); svc.RunWorkers(workersCtx) }()
	defer func() { stopWorkers(); <-workersDone }()

	grpcSrv, _ := grpcx.NewServer() // standard gRPC health service only (WS0' §4)
	return server.Run(ctx, server.Config{
		HTTP: &http.Server{
			Addr:              cfg.HTTPAddr,
			Handler:           otelx.HTTPHandler(newHandler(logger, readinessChecks(pool, producer), svc.API), cfg.ServiceName),
			ReadHeaderTimeout: 10 * time.Second,
		},
		GRPC:            grpcSrv,
		GRPCAddr:        cfg.GRPCAddr,
		ShutdownTimeout: cfg.ShutdownTimeout,
		Logger:          logger,
	})
}
