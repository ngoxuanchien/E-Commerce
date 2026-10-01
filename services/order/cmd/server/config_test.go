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
	assert.Equal(t, []string{"VNPAY", "STRIPE"}, cfg.OnlinePaymentMethods)
	assert.Equal(t, int32(500), cfg.CommissionRateBps)
	assert.Equal(t, 30*time.Minute, cfg.PaymentTimeout)
	assert.Equal(t, 72*time.Hour, cfg.AutoCompleteAfter)
	assert.NoError(t, cfg.validate())
}

func TestConfigRequiresCatalogAddr(t *testing.T) {
	t.Setenv("SERVICE_NAME", "order")
	t.Setenv("CATALOG_GRPC_ADDR", "")
	_, err := config.Load[Config]()
	assert.ErrorContains(t, err, "CATALOG_GRPC_ADDR")
}

func TestConfigOnlinePaymentMethods(t *testing.T) {
	load := func(t *testing.T, methods string) Config {
		t.Helper()
		t.Setenv("SERVICE_NAME", "order")
		t.Setenv("CATALOG_GRPC_ADDR", "catalog:9082")
		t.Setenv("ONLINE_PAYMENT_METHODS", methods)
		cfg, err := config.Load[Config]()
		require.NoError(t, err)
		return cfg
	}

	t.Run("an unknown method is rejected", func(t *testing.T) {
		cfg := load(t, "VNPAY, CASH")
		assert.ErrorContains(t, cfg.validate(), "ONLINE_PAYMENT_METHODS")
	})
	t.Run("an empty list is valid and means COD only", func(t *testing.T) {
		cfg := load(t, "")
		assert.Empty(t, cfg.onlineMethods())
		assert.NoError(t, cfg.validate())
	})
	t.Run("entries are trimmed and blanks dropped", func(t *testing.T) {
		cfg := load(t, " STRIPE , ,VNPAY ")
		assert.Equal(t, []string{"STRIPE", "VNPAY"}, cfg.onlineMethods())
		assert.NoError(t, cfg.validate())
	})
}

func TestConfigCommissionRateBpsOutOfRange(t *testing.T) {
	t.Setenv("SERVICE_NAME", "order")
	t.Setenv("CATALOG_GRPC_ADDR", "catalog:9082")
	t.Setenv("COMMISSION_RATE_BPS", "10001")
	cfg, err := config.Load[Config]()
	require.NoError(t, err)
	assert.ErrorContains(t, cfg.validate(), "COMMISSION_RATE_BPS")
}

func TestConfigValidate(t *testing.T) {
	ok := Config{
		ShippingFeeFlat: 0, SweeperInterval: time.Second, PendingTimeout: time.Second,
		CommissionRateBps: 500, PaymentTimeout: time.Minute, AutoCompleteAfter: time.Hour,
	}
	require.NoError(t, ok.validate())
	for name, mut := range map[string]func(*Config){
		"negative fee":        func(c *Config) { c.ShippingFeeFlat = -1 },
		"zero interval":       func(c *Config) { c.SweeperInterval = 0 },
		"zero timeout":        func(c *Config) { c.PendingTimeout = 0 },
		"negative rate":       func(c *Config) { c.CommissionRateBps = -1 },
		"rate above 100%":     func(c *Config) { c.CommissionRateBps = 10_001 },
		"zero pay timeout":    func(c *Config) { c.PaymentTimeout = 0 },
		"zero complete after": func(c *Config) { c.AutoCompleteAfter = 0 },
		"unknown method":      func(c *Config) { c.OnlinePaymentMethods = []string{"MOMO"} },
		"unknown after trim":  func(c *Config) { c.OnlinePaymentMethods = []string{" VNPAY ", "MOMO"} },
	} {
		c := ok
		mut(&c)
		assert.Error(t, c.validate(), name)
	}
}
