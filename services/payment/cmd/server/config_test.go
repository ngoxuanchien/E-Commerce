package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/config"
)

func TestConfigRequiresServiceName(t *testing.T) {
	t.Setenv("SERVICE_NAME", "")
	_, err := config.Load[Config]()
	assert.Error(t, err)
	t.Setenv("SERVICE_NAME", "payment")
	cfg, err := config.Load[Config]()
	require.NoError(t, err)
	assert.Equal(t, "payment", cfg.ServiceName)
}

func TestConfigDefaultsAndTypes(t *testing.T) {
	for _, k := range []string{"PROVIDER_VNPAY", "PROVIDER_STRIPE", "FAKE_WEBHOOK_DELAY", "RECONCILE_INTERVAL",
		"RECONCILE_MIN_AGE", "REFUND_MAX_ATTEMPTS", "STRIPE_MIN_AMOUNT"} {
		t.Setenv(k, "")
	}
	t.Setenv("SERVICE_NAME", "payment")
	cfg, err := config.Load[Config]()
	require.NoError(t, err)
	assert.Equal(t, "fake", cfg.ProviderVNPay)
	assert.Equal(t, "fake", cfg.ProviderStripe)
	assert.Equal(t, 500*time.Millisecond, cfg.FakeWebhookDelay)
	assert.Equal(t, time.Minute, cfg.ReconcileInterval)
	assert.Equal(t, 2*time.Minute, cfg.ReconcileMinAge)
	assert.Equal(t, 8, cfg.RefundMaxAttempts)
	assert.Equal(t, int64(10000), cfg.StripeMinAmount)
}

func TestConfigParsesPaymentVariables(t *testing.T) {
	t.Setenv("SERVICE_NAME", "payment")
	t.Setenv("FAKE_WEBHOOK_DELAY", "2s")
	t.Setenv("RECONCILE_INTERVAL", "10s")
	t.Setenv("RECONCILE_MIN_AGE", "30s")
	t.Setenv("REFUND_MAX_ATTEMPTS", "3")
	t.Setenv("STRIPE_MIN_AMOUNT", "12000")
	cfg, err := config.Load[Config]()
	require.NoError(t, err)
	assert.Equal(t, 2*time.Second, cfg.FakeWebhookDelay)
	assert.Equal(t, 10*time.Second, cfg.ReconcileInterval)
	assert.Equal(t, 30*time.Second, cfg.ReconcileMinAge)
	assert.Equal(t, 3, cfg.RefundMaxAttempts)
	assert.Equal(t, int64(12000), cfg.StripeMinAmount)

	t.Setenv("REFUND_MAX_ATTEMPTS", "many")
	_, err = config.Load[Config]()
	assert.Error(t, err)
}

func validConfig() Config {
	var c Config
	c.ServiceName = "payment"
	c.OrderGRPCAddr = "order:9083"
	c.PublicBaseURL = "http://localhost:3000"
	c.DatabaseURL = "postgres://x"
	c.KafkaBrokers = []string{"kafka:9092"}
	c.ProviderVNPay, c.ProviderStripe = "fake", "fake"
	c.FakeWebhookURL, c.FakeWebhookSecret = "http://gateway/hook", "secret"
	c.ReconcileInterval, c.ReconcileMinAge = time.Minute, 2*time.Minute
	c.RefundMaxAttempts = 8
	return c
}

func TestConfigValidate(t *testing.T) {
	realVNPay := func(c *Config) {
		c.ProviderVNPay = "vnpay"
		c.VNPayTmnCode, c.VNPayHashSecret = "tmn", "hash"
		c.VNPayPayURL, c.VNPayAPIURL = "https://pay", "https://api"
	}
	realStripe := func(c *Config) {
		c.ProviderStripe = "stripe"
		c.StripeSecretKey, c.StripeWebhookSecret = "sk_test_x", "whsec_x"
	}
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantErr string // "" = valid
	}{
		{"both fake", func(*Config) {}, ""},
		{"vnpay real, stripe fake", realVNPay, ""},
		{"stripe real, vnpay fake", realStripe, ""},
		{"both real needs no fake settings", func(c *Config) {
			realVNPay(c)
			realStripe(c)
			c.FakeWebhookURL, c.FakeWebhookSecret = "", ""
		}, ""},
		{"missing ORDER_GRPC_ADDR", func(c *Config) { c.OrderGRPCAddr = "" }, "ORDER_GRPC_ADDR"},
		{"missing PUBLIC_BASE_URL", func(c *Config) { c.PublicBaseURL = "" }, "PUBLIC_BASE_URL"},
		{"missing DATABASE_URL", func(c *Config) { c.DatabaseURL = "" }, "DATABASE_URL"},
		{"missing KAFKA_BROKERS", func(c *Config) { c.KafkaBrokers = nil }, "KAFKA_BROKERS"},
		{"bad PROVIDER_VNPAY", func(c *Config) { c.ProviderVNPay = "stripe" }, "PROVIDER_VNPAY"},
		{"bad PROVIDER_STRIPE", func(c *Config) { c.ProviderStripe = "vnpay" }, "PROVIDER_STRIPE"},
		{"empty PROVIDER_STRIPE", func(c *Config) { c.ProviderStripe = "" }, "PROVIDER_STRIPE"},
		{"fake without webhook url", func(c *Config) { c.FakeWebhookURL = "" }, "FAKE_WEBHOOK_URL"},
		{"fake without webhook secret", func(c *Config) { c.FakeWebhookSecret = "" }, "FAKE_WEBHOOK_SECRET"},
		{"one fake still needs secret", func(c *Config) { realVNPay(c); c.FakeWebhookSecret = "" }, "FAKE_WEBHOOK_SECRET"},
		{"vnpay without tmn code", func(c *Config) { realVNPay(c); c.VNPayTmnCode = "" }, "VNPAY_TMN_CODE"},
		{"vnpay without hash secret", func(c *Config) { realVNPay(c); c.VNPayHashSecret = "" }, "VNPAY_HASH_SECRET"},
		{"vnpay without pay url", func(c *Config) { realVNPay(c); c.VNPayPayURL = "" }, "VNPAY_PAY_URL"},
		{"vnpay without api url", func(c *Config) { realVNPay(c); c.VNPayAPIURL = "" }, "VNPAY_API_URL"},
		{"stripe without secret key", func(c *Config) { realStripe(c); c.StripeSecretKey = "" }, "STRIPE_SECRET_KEY"},
		{"stripe without webhook secret", func(c *Config) { realStripe(c); c.StripeWebhookSecret = "" }, "STRIPE_WEBHOOK_SECRET"},
		{"zero reconcile interval", func(c *Config) { c.ReconcileInterval = 0 }, "RECONCILE_INTERVAL"},
		{"min age at the floor", func(c *Config) { c.ReconcileMinAge = 30 * time.Minute }, "RECONCILE_MIN_AGE"},
		{"min age just under the floor", func(c *Config) { c.ReconcileMinAge = 30*time.Minute - time.Second }, ""},
		{"zero refund attempts", func(c *Config) { c.RefundMaxAttempts = 0 }, "REFUND_MAX_ATTEMPTS"},
		{"negative stripe minimum", func(c *Config) { c.StripeMinAmount = -1 }, "STRIPE_MIN_AMOUNT"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validConfig()
			tc.mutate(&c)
			err := c.validate()
			if tc.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tc.wantErr)
		})
	}
}
