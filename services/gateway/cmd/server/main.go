// Command server runs the API gateway.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ngoxuanchien/e-commerce/pkg/config"
	"github.com/ngoxuanchien/e-commerce/pkg/logx"
	"github.com/ngoxuanchien/e-commerce/pkg/otelx"
	"github.com/ngoxuanchien/e-commerce/pkg/server"
	"github.com/ngoxuanchien/e-commerce/services/gateway/internal/auth"
	"github.com/ngoxuanchien/e-commerce/services/gateway/internal/ratelimit"
	"github.com/ngoxuanchien/e-commerce/services/gateway/internal/routes"
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
	logger := logx.New(os.Stdout, cfg.LogLevel).With("service", cfg.ServiceName)
	slog.SetDefault(logger)

	shutdownOtel, err := otelx.Setup(ctx, cfg.ServiceName, cfg.OTLPEndpoint)
	if err != nil {
		return err
	}
	defer func() { _ = shutdownOtel(context.Background()) }()

	upstreams, err := newUpstreams(cfg)
	if err != nil {
		return err
	}
	clientIP, err := ratelimit.ParseTrustedProxies(cfg.TrustedProxies)
	if err != nil {
		return err
	}

	jwks := auth.NewJWKS(cfg.JWKSURL)
	general, login := ratelimit.NewGeneralLimiter(), ratelimit.NewLoginLimiter()
	go jwks.Run(ctx, logger)
	go general.Run(ctx, time.Minute)
	go login.Run(ctx, time.Minute)

	handler, err := newHandler(&gateway{
		logger:    logger,
		checks:    map[string]server.Check{"jwks": jwks.Ready},
		routes:    routes.Default(),
		upstreams: upstreams,
		verifier:  auth.NewVerifier(jwks),
		origins:   cfg.CORSAllowedOrigins,
		general:   general,
		login:     login,
		clientIP:  clientIP.Resolve,
	})
	if err != nil {
		return err
	}

	return server.Run(ctx, server.Config{
		HTTP: &http.Server{
			Addr:              cfg.HTTPAddr,
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
		},
		ShutdownTimeout: cfg.ShutdownTimeout,
		Logger:          logger,
	})
}
