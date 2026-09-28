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

	"github.com/ngoxuanchien/e-commerce/pkg/config"
	"github.com/ngoxuanchien/e-commerce/pkg/grpcx"
	"github.com/ngoxuanchien/e-commerce/pkg/logx"
	"github.com/ngoxuanchien/e-commerce/pkg/otelx"
	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/pkg/server"
	"github.com/ngoxuanchien/e-commerce/services/order/migrations"
)

// Config is the service configuration; service-specific fields are added by its workstream.
type Config struct {
	config.Base
}

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

	checks := map[string]server.Check{"postgres": pool.Ping}
	grpcSrv, _ := grpcx.NewServer()

	return server.Run(ctx, server.Config{
		HTTP: &http.Server{
			Addr:              cfg.HTTPAddr,
			Handler:           otelx.HTTPHandler(newHandler(logger, checks, nil), cfg.ServiceName),
			ReadHeaderTimeout: 10 * time.Second,
		},
		GRPC:            grpcSrv,
		GRPCAddr:        cfg.GRPCAddr,
		ShutdownTimeout: cfg.ShutdownTimeout,
		Logger:          logger,
	})
}
