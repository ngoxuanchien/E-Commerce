// Command server runs order-svc.
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

	"github.com/redis/go-redis/v9"

	"github.com/ngoxuanchien/e-commerce/pkg/config"
	"github.com/ngoxuanchien/e-commerce/pkg/grpcx"
	"github.com/ngoxuanchien/e-commerce/pkg/kafkax"
	"github.com/ngoxuanchien/e-commerce/pkg/logx"
	"github.com/ngoxuanchien/e-commerce/pkg/otelx"
	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/pkg/server"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/catalog"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/bootstrap"
	"github.com/ngoxuanchien/e-commerce/services/order/migrations"
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

	redisOpts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("parse REDIS_URL: %w", err)
	}
	rdb := redis.NewClient(redisOpts)
	defer func() { _ = rdb.Close() }()

	producer, err := kafkax.NewProducer(cfg.KafkaBrokers)
	if err != nil {
		return err
	}
	defer producer.Close()

	catalogConn, err := grpcx.Dial(cfg.CatalogGRPCAddr)
	if err != nil {
		return fmt.Errorf("dial catalog: %w", err)
	}
	defer func() { _ = catalogConn.Close() }()

	svc := bootstrap.New(bootstrap.Config{
		ShippingFeeFlat: cfg.ShippingFeeFlat, SweeperInterval: cfg.SweeperInterval,
		PendingTimeout: cfg.PendingTimeout, Catalog: catalog.DefaultConfig(),
	}, bootstrap.Deps{Pool: pool, Redis: rdb, CatalogConn: catalogConn, Producer: producer, Logger: logger})

	// Workers stop (deferred last, so run first) before the connections close.
	workersCtx, stopWorkers := context.WithCancel(ctx)
	workersDone := make(chan struct{})
	go func() { defer close(workersDone); svc.RunWorkers(workersCtx) }()
	defer func() { stopWorkers(); <-workersDone }()

	grpcSrv, _ := grpcx.NewServer()
	return server.Run(ctx, server.Config{
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
}
