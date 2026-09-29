// Command server runs identity-svc.
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

	identityv1 "github.com/ngoxuanchien/e-commerce/gen/identity/v1"
	"github.com/ngoxuanchien/e-commerce/pkg/config"
	"github.com/ngoxuanchien/e-commerce/pkg/grpcx"
	"github.com/ngoxuanchien/e-commerce/pkg/logx"
	"github.com/ngoxuanchien/e-commerce/pkg/otelx"
	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/pkg/server"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/token"
	"github.com/ngoxuanchien/e-commerce/services/identity/migrations"
)

// Config is identity-svc's configuration (WS0 base + WS1 §4).
type Config struct {
	config.Base
	JWTPrivateKeyPath string        `env:"JWT_PRIVATE_KEY_PATH,required,notEmpty"`
	AccessTokenTTL    time.Duration `env:"ACCESS_TOKEN_TTL" envDefault:"15m"`
	RefreshTokenTTL   time.Duration `env:"REFRESH_TOKEN_TTL" envDefault:"720h"`
	CookieSecure      bool          `env:"COOKIE_SECURE" envDefault:"true"`
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

	key, err := token.LoadPrivateKey(cfg.JWTPrivateKeyPath)
	if err != nil {
		return err
	}

	if err := pgxx.Migrate(cfg.DatabaseURL, migrations.FS); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	pool, err := pgxx.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	comps, err := newComponents(pool, key, cfg, app.DefaultBcryptCost, logger)
	if err != nil {
		return err
	}

	checks := map[string]server.Check{"postgres": pool.Ping}
	grpcSrv, _ := grpcx.NewServer()
	identityv1.RegisterIdentityInternalServer(grpcSrv, comps.GRPC)

	return server.Run(ctx, server.Config{
		HTTP: &http.Server{
			Addr:              cfg.HTTPAddr,
			Handler:           otelx.HTTPHandler(newHandler(logger, checks, comps.API), cfg.ServiceName),
			ReadHeaderTimeout: 10 * time.Second,
		},
		GRPC:            grpcSrv,
		GRPCAddr:        cfg.GRPCAddr,
		ShutdownTimeout: cfg.ShutdownTimeout,
		Logger:          logger,
	})
}
