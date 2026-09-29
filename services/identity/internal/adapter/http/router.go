// Package httpapi serves identity-svc's REST API (api/openapi/identity.yaml).
package httpapi

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ngoxuanchien/e-commerce/pkg/authctx"
	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/token"
)

// Config holds HTTP-layer settings.
type Config struct {
	CookieSecure bool          // false only for local development over plain HTTP
	RefreshTTL   time.Duration // Max-Age of the refresh cookie
}

type handler struct {
	auth *app.AuthService
	jwks token.JWKS
	cfg  Config
}

// NewRouter returns the REST API. authctx.Middleware must run in front of it.
func NewRouter(auth *app.AuthService, jwks token.JWKS, cfg Config) http.Handler {
	h := &handler{auth: auth, jwks: jwks, cfg: cfg}
	notFound := httpx.NotFound().ServeHTTP

	r := chi.NewRouter()
	r.NotFound(notFound)
	r.MethodNotAllowed(notFound)
	r.Post("/api/auth/register", h.register)
	r.Post("/api/auth/login", h.login)
	r.Post("/api/auth/refresh", h.refresh)
	r.Post("/api/auth/logout", h.logout)
	r.With(authctx.RequireUser).Get("/api/users/me", h.me)
	r.Get("/.well-known/jwks.json", h.getJWKS)
	return r
}
