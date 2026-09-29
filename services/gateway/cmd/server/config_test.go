package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/config"
)

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("SERVICE_NAME", "gateway")
	t.Setenv("IDENTITY_URL", "http://identity:8081")
	t.Setenv("CATALOG_URL", "http://catalog:8082")
	t.Setenv("ORDER_URL", "http://order:8083")
	t.Setenv("IDENTITY_JWKS_URL", "http://identity:8081/.well-known/jwks.json")
}

func TestLoadConfig(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("CORS_ALLOWED_ORIGINS", "http://localhost:5173,https://shop.example")
	t.Setenv("UPSTREAM_TIMEOUT", "3s")
	t.Setenv("TRUSTED_PROXIES", "10.0.0.0/8,192.168.1.1")

	cfg, err := config.Load[Config]()
	require.NoError(t, err)

	assert.Equal(t, "http://identity:8081", cfg.IdentityURL)
	assert.Equal(t, "http://catalog:8082", cfg.CatalogURL)
	assert.Equal(t, "http://order:8083", cfg.OrderURL)
	assert.Equal(t, "http://identity:8081/.well-known/jwks.json", cfg.JWKSURL)
	assert.Equal(t, []string{"http://localhost:5173", "https://shop.example"}, cfg.CORSAllowedOrigins)
	assert.Equal(t, 3*time.Second, cfg.UpstreamTimeout)
	assert.Equal(t, []string{"10.0.0.0/8", "192.168.1.1"}, cfg.TrustedProxies)
}

func TestLoadConfigDefaults(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := config.Load[Config]()
	require.NoError(t, err)

	assert.Equal(t, 10*time.Second, cfg.UpstreamTimeout)
	assert.Empty(t, cfg.CORSAllowedOrigins)
	assert.Empty(t, cfg.TrustedProxies)
}

func TestLoadConfigRequiresUpstreamsAndJWKS(t *testing.T) {
	for _, name := range []string{"IDENTITY_URL", "CATALOG_URL", "ORDER_URL", "IDENTITY_JWKS_URL"} {
		t.Run(name, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv(name, "")

			_, err := config.Load[Config]()

			require.Error(t, err)
			assert.Contains(t, err.Error(), name)
		})
	}
}

func TestNewUpstreams(t *testing.T) {
	valid := Config{
		IdentityURL:     "http://identity:8081",
		CatalogURL:      "http://catalog:8082",
		OrderURL:        "http://order:8083",
		UpstreamTimeout: time.Second,
	}

	ups, err := newUpstreams(valid)
	require.NoError(t, err)
	assert.Len(t, ups, 3)

	bad := valid
	bad.IdentityURL = "identity:8081"
	_, err = newUpstreams(bad)
	assert.Error(t, err)
}
