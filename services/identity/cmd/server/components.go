package main

import (
	"crypto/rsa"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	grpcapi "github.com/ngoxuanchien/e-commerce/services/identity/internal/adapter/grpc"
	httpapi "github.com/ngoxuanchien/e-commerce/services/identity/internal/adapter/http"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/adapter/postgres"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/token"
)

// components are identity-svc's adapters wired to their dependencies.
type components struct {
	API  http.Handler
	GRPC *grpcapi.Server
}

func newComponents(pool *pgxpool.Pool, key *rsa.PrivateKey, cfg Config, bcryptCost int, logger *slog.Logger) (components, error) {
	store := postgres.NewStore(pool)
	signer := token.NewSigner(key, cfg.AccessTokenTTL)
	auth, err := app.NewAuthService(store, signer, app.AuthConfig{
		RefreshTTL: cfg.RefreshTokenTTL, BcryptCost: bcryptCost, Logger: logger,
	})
	if err != nil {
		return components{}, err
	}
	return components{
		API:  httpapi.NewRouter(auth, signer.JWKS(), httpapi.Config{CookieSecure: cfg.CookieSecure, RefreshTTL: cfg.RefreshTokenTTL}),
		GRPC: grpcapi.NewServer(app.NewRoleService(store), logger),
	}, nil
}
