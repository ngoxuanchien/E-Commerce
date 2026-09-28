// Command server runs catalog-svc.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	catalogv1 "github.com/ngoxuanchien/e-commerce/gen/catalog/v1"
	"github.com/ngoxuanchien/e-commerce/pkg/config"
	"github.com/ngoxuanchien/e-commerce/pkg/grpcx"
	"github.com/ngoxuanchien/e-commerce/pkg/kafkax"
	"github.com/ngoxuanchien/e-commerce/pkg/logx"
	"github.com/ngoxuanchien/e-commerce/pkg/otelx"
	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/pkg/server"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/adapter/grpcapi"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/adapter/httpapi"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/adapter/kafka"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/adapter/postgres"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/adapter/storage"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
	"github.com/ngoxuanchien/e-commerce/services/catalog/migrations"
)

// Config is catalog-svc's configuration (WS0 §3 + WS2 §4).
type Config struct {
	config.Base
	IdentityGRPCAddr   string `env:"IDENTITY_GRPC_ADDR,required,notEmpty"`
	MinIOEndpoint      string `env:"MINIO_ENDPOINT,required,notEmpty"`
	MinIOAccessKey     string `env:"MINIO_ACCESS_KEY,required,notEmpty"`
	MinIOSecretKey     string `env:"MINIO_SECRET_KEY,required,notEmpty"`
	MinIOBucket        string `env:"MINIO_BUCKET" envDefault:"product-images"`
	MinIOUseSSL        bool   `env:"MINIO_USE_SSL" envDefault:"false"`
	PublicAssetBaseURL string `env:"PUBLIC_ASSET_BASE_URL,required,notEmpty"`
}

// validate checks base fields that are optional for other services.
func (c Config) validate() error {
	var errs []error
	brokers := slices.DeleteFunc(slices.Clone(c.KafkaBrokers), func(b string) bool { return strings.TrimSpace(b) == "" })
	if len(brokers) == 0 {
		errs = append(errs, errors.New("KAFKA_BROKERS is required"))
	}
	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}
	return errors.Join(errs...)
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
	store := postgres.New(pool)

	identityConn, err := grpcx.Dial(cfg.IdentityGRPCAddr)
	if err != nil {
		return fmt.Errorf("dial identity: %w", err)
	}
	defer func() { _ = identityConn.Close() }()

	presigner, err := storage.New(storage.Config{
		Endpoint: cfg.MinIOEndpoint, AccessKey: cfg.MinIOAccessKey, SecretKey: cfg.MinIOSecretKey,
		Bucket: cfg.MinIOBucket, UseSSL: cfg.MinIOUseSSL, PublicBaseURL: cfg.PublicAssetBaseURL,
	})
	if err != nil {
		return err
	}
	kafkaPing, err := kafkax.NewProducer(cfg.KafkaBrokers)
	if err != nil {
		return err
	}
	defer kafkaPing.Close()

	assets := domain.AssetBase(cfg.PublicAssetBaseURL)
	inventory := app.NewInventoryService(store)
	api := httpapi.NewRouter(httpapi.Deps{
		Shops:    app.NewShopService(store, grpcapi.NewIdentityClient(identityConn)),
		Products: app.NewProductService(store, presigner),
		Search:   app.NewSearchService(store),
		Assets:   assets,
	})

	grpcSrv, _ := grpcx.NewServer()
	catalogv1.RegisterCatalogInternalServer(grpcSrv, grpcapi.NewCatalogServer(inventory, assets))

	consumer, err := kafka.NewConsumer(cfg.KafkaBrokers, kafka.NewHandler(inventory, logger), logger)
	if err != nil {
		return err
	}
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		if err := consumer.Run(ctx); err != nil {
			logger.Error("order events consumer stopped", "error", err)
		}
	}()
	defer func() {
		stop() // make sure the consumer sees cancellation even if server.Run failed
		<-consumerDone
		consumer.Close()
	}()

	checks := map[string]server.Check{
		"postgres": store.Ping,
		"minio":    presigner.Ping,
		"kafka":    kafkaPing.Ping,
	}
	return server.Run(ctx, server.Config{
		HTTP: &http.Server{
			Addr:              cfg.HTTPAddr,
			Handler:           otelx.HTTPHandler(newHandler(logger, checks, api), cfg.ServiceName),
			ReadHeaderTimeout: 10 * time.Second,
		},
		GRPC:            grpcSrv,
		GRPCAddr:        cfg.GRPCAddr,
		ShutdownTimeout: cfg.ShutdownTimeout,
		Logger:          logger,
	})
}
