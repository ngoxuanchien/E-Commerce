package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/config"
)

func TestConfigDefaults(t *testing.T) {
	t.Setenv("SERVICE_NAME", "order")
	t.Setenv("CATALOG_GRPC_ADDR", "catalog:9082")
	cfg, err := config.Load[Config]()
	require.NoError(t, err)
	assert.Equal(t, "catalog:9082", cfg.CatalogGRPCAddr)
	assert.Equal(t, int64(30_000), cfg.ShippingFeeFlat)
	assert.Equal(t, 60*time.Second, cfg.SweeperInterval)
	assert.Equal(t, 2*time.Minute, cfg.PendingTimeout)
	assert.NoError(t, cfg.validate())
}

func TestConfigRequiresCatalogAddr(t *testing.T) {
	t.Setenv("SERVICE_NAME", "order")
	t.Setenv("CATALOG_GRPC_ADDR", "")
	_, err := config.Load[Config]()
	assert.ErrorContains(t, err, "CATALOG_GRPC_ADDR")
}

func TestConfigValidate(t *testing.T) {
	ok := Config{ShippingFeeFlat: 0, SweeperInterval: time.Second, PendingTimeout: time.Second}
	require.NoError(t, ok.validate())
	for name, mut := range map[string]func(*Config){
		"negative fee":  func(c *Config) { c.ShippingFeeFlat = -1 },
		"zero interval": func(c *Config) { c.SweeperInterval = 0 },
		"zero timeout":  func(c *Config) { c.PendingTimeout = 0 },
	} {
		c := ok
		mut(&c)
		assert.Error(t, c.validate(), name)
	}
}
