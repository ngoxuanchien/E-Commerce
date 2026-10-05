package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/ngoxuanchien/e-commerce/pkg/config"
	"github.com/ngoxuanchien/e-commerce/services/gateway/internal/proxy"
	"github.com/ngoxuanchien/e-commerce/services/gateway/internal/routes"
)

// Config is the gateway configuration.
type Config struct {
	config.Base

	IdentityURL string `env:"IDENTITY_URL,required,notEmpty"`
	CatalogURL  string `env:"CATALOG_URL,required,notEmpty"`
	OrderURL    string `env:"ORDER_URL,required,notEmpty"`
	PaymentURL  string `env:"PAYMENT_URL,required,notEmpty"`
	LedgerURL   string `env:"LEDGER_URL,required,notEmpty"`
	JWKSURL     string `env:"IDENTITY_JWKS_URL,required,notEmpty"`

	CORSAllowedOrigins []string      `env:"CORS_ALLOWED_ORIGINS" envSeparator:","`
	UpstreamTimeout    time.Duration `env:"UPSTREAM_TIMEOUT" envDefault:"10s"`
	// TrustedProxies lists CIDRs or IPs whose X-Forwarded-For is honored;
	// empty means every client is identified by its remote address.
	TrustedProxies []string `env:"TRUSTED_PROXIES" envSeparator:","`
}

// newUpstreams builds one reverse proxy per upstream service, all sharing a
// single transport (and so one connection pool).
func newUpstreams(cfg Config) (map[string]http.Handler, error) {
	transport := proxy.NewTransport(cfg.UpstreamTimeout)
	upstreams := make(map[string]http.Handler, 5)
	for name, raw := range map[string]string{
		routes.Identity: cfg.IdentityURL,
		routes.Catalog:  cfg.CatalogURL,
		routes.Order:    cfg.OrderURL,
		routes.Payment:  cfg.PaymentURL,
		routes.Ledger:   cfg.LedgerURL,
	} {
		target, err := proxy.ParseTarget(raw)
		if err != nil {
			return nil, fmt.Errorf("%s upstream: %w", name, err)
		}
		upstreams[name] = proxy.New(target, transport)
	}
	return upstreams, nil
}
