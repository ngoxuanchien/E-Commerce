package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/ngoxuanchien/e-commerce/pkg/config"
)

// Provider names accepted by PROVIDER_VNPAY and PROVIDER_STRIPE.
const (
	providerFake   = "fake"
	providerVNPay  = "vnpay"
	providerStripe = "stripe"
)

// paymentTimeoutFloor is order-svc's PAYMENT_TIMEOUT floor (30 minutes). A payment is never
// younger than RECONCILE_MIN_AGE when it first becomes interesting, so the reconciler must start
// polling well before the shortest payment window ends.
const paymentTimeoutFloor = 30 * time.Minute

// Config is payment-svc's configuration: the WS0 base plus the WS0' §8 variables.
type Config struct {
	config.Base

	OrderGRPCAddr string `env:"ORDER_GRPC_ADDR"`
	PublicBaseURL string `env:"PUBLIC_BASE_URL"`

	ProviderVNPay  string `env:"PROVIDER_VNPAY" envDefault:"fake"`
	ProviderStripe string `env:"PROVIDER_STRIPE" envDefault:"fake"`

	FakeWebhookURL    string        `env:"FAKE_WEBHOOK_URL"`
	FakeWebhookSecret string        `env:"FAKE_WEBHOOK_SECRET"`
	FakeWebhookDelay  time.Duration `env:"FAKE_WEBHOOK_DELAY" envDefault:"500ms"`

	ReconcileInterval time.Duration `env:"RECONCILE_INTERVAL" envDefault:"60s"`
	ReconcileMinAge   time.Duration `env:"RECONCILE_MIN_AGE" envDefault:"2m"`
	RefundMaxAttempts int           `env:"REFUND_MAX_ATTEMPTS" envDefault:"8"`

	VNPayTmnCode    string `env:"VNPAY_TMN_CODE"`
	VNPayHashSecret string `env:"VNPAY_HASH_SECRET"`
	VNPayPayURL     string `env:"VNPAY_PAY_URL"`
	VNPayAPIURL     string `env:"VNPAY_API_URL"`

	StripeSecretKey     string `env:"STRIPE_SECRET_KEY"`
	StripeWebhookSecret string `env:"STRIPE_WEBHOOK_SECRET"`
	StripeMinAmount     int64  `env:"STRIPE_MIN_AMOUNT" envDefault:"10000"`
}

// validate reports the first missing or inconsistent setting.
func (c Config) validate() error {
	required := []struct{ name, value string }{
		{"ORDER_GRPC_ADDR", c.OrderGRPCAddr},
		{"PUBLIC_BASE_URL", c.PublicBaseURL},
		{"DATABASE_URL", c.DatabaseURL},
	}
	for _, r := range required {
		if r.value == "" {
			return fmt.Errorf("%s is required", r.name)
		}
	}
	if len(c.KafkaBrokers) == 0 {
		return errors.New("KAFKA_BROKERS is required")
	}
	if c.ProviderVNPay != providerFake && c.ProviderVNPay != providerVNPay {
		return fmt.Errorf("PROVIDER_VNPAY must be %s or %s, got %q", providerFake, providerVNPay, c.ProviderVNPay)
	}
	if c.ProviderStripe != providerFake && c.ProviderStripe != providerStripe {
		return fmt.Errorf("PROVIDER_STRIPE must be %s or %s, got %q", providerFake, providerStripe, c.ProviderStripe)
	}
	if c.usesFake() {
		if c.FakeWebhookURL == "" {
			return errors.New("FAKE_WEBHOOK_URL is required when a provider is fake")
		}
		if c.FakeWebhookSecret == "" {
			return errors.New("FAKE_WEBHOOK_SECRET is required when a provider is fake")
		}
	}
	if c.ProviderVNPay == providerVNPay {
		vn := []struct{ name, value string }{
			{"VNPAY_TMN_CODE", c.VNPayTmnCode}, {"VNPAY_HASH_SECRET", c.VNPayHashSecret},
			{"VNPAY_PAY_URL", c.VNPayPayURL}, {"VNPAY_API_URL", c.VNPayAPIURL},
		}
		for _, r := range vn {
			if r.value == "" {
				return fmt.Errorf("%s is required when PROVIDER_VNPAY=vnpay", r.name)
			}
		}
	}
	if c.ProviderStripe == providerStripe {
		switch {
		case c.StripeSecretKey == "":
			return errors.New("STRIPE_SECRET_KEY is required when PROVIDER_STRIPE=stripe")
		case c.StripeWebhookSecret == "":
			return errors.New("STRIPE_WEBHOOK_SECRET is required when PROVIDER_STRIPE=stripe")
		}
	}
	switch {
	case c.ReconcileInterval <= 0:
		return errors.New("RECONCILE_INTERVAL must be > 0")
	case c.ReconcileMinAge < 0 || c.ReconcileMinAge >= paymentTimeoutFloor:
		return fmt.Errorf("RECONCILE_MIN_AGE must be >= 0 and < %s (the PAYMENT_TIMEOUT floor)", paymentTimeoutFloor)
	case c.RefundMaxAttempts <= 0:
		return errors.New("REFUND_MAX_ATTEMPTS must be > 0")
	case c.StripeMinAmount < 0:
		return errors.New("STRIPE_MIN_AMOUNT must be >= 0")
	}
	return nil
}

func (c Config) usesFake() bool {
	return c.ProviderVNPay == providerFake || c.ProviderStripe == providerFake
}
