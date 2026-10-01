package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ngoxuanchien/e-commerce/pkg/config"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

// Config is the service configuration (WS0 base + WS3 §6 variables).
type Config struct {
	config.Base
	CatalogGRPCAddr string        `env:"CATALOG_GRPC_ADDR,required,notEmpty"`
	ShippingFeeFlat int64         `env:"SHIPPING_FEE_FLAT" envDefault:"30000"`
	SweeperInterval time.Duration `env:"SWEEPER_INTERVAL" envDefault:"60s"`
	PendingTimeout  time.Duration `env:"PENDING_TIMEOUT" envDefault:"2m"`

	// WS3' online payment (P2).
	OnlinePaymentMethods []string      `env:"ONLINE_PAYMENT_METHODS" envDefault:"VNPAY,STRIPE" envSeparator:","`
	CommissionRateBps    int32         `env:"COMMISSION_RATE_BPS" envDefault:"500"`
	PaymentTimeout       time.Duration `env:"PAYMENT_TIMEOUT" envDefault:"30m"`
	AutoCompleteAfter    time.Duration `env:"AUTO_COMPLETE_AFTER" envDefault:"72h"`
}

// trimmedMethods returns the parsed ONLINE_PAYMENT_METHODS with each entry
// trimmed and empty ones dropped.
func (c Config) trimmedMethods() []string {
	out := make([]string, 0, len(c.OnlinePaymentMethods))
	for _, m := range c.OnlinePaymentMethods {
		if m = strings.TrimSpace(m); m != "" {
			out = append(out, m)
		}
	}
	return out
}

// onlineMethods is the enabled online payment methods; an empty list means COD
// only. An ONLINE_PAYMENT_METHODS that is set but blank means COD only, which
// needs the environment lookup: caarlos0/env substitutes envDefault for an
// explicitly empty value, so parsing cannot tell "" from unset.
func (c Config) onlineMethods() []string {
	if raw, set := os.LookupEnv("ONLINE_PAYMENT_METHODS"); set && strings.TrimSpace(raw) == "" {
		return nil
	}
	return c.trimmedMethods()
}

func (c Config) validate() error {
	switch {
	case c.ShippingFeeFlat < 0:
		return errors.New("SHIPPING_FEE_FLAT must be >= 0")
	case c.SweeperInterval <= 0:
		return errors.New("SWEEPER_INTERVAL must be > 0")
	case c.PendingTimeout <= 0:
		return errors.New("PENDING_TIMEOUT must be > 0")
	}
	for _, m := range c.trimmedMethods() {
		if m != domain.PaymentVNPay && m != domain.PaymentStripe {
			return fmt.Errorf("ONLINE_PAYMENT_METHODS must only list %s or %s, got %q",
				domain.PaymentVNPay, domain.PaymentStripe, m)
		}
	}
	switch {
	case c.CommissionRateBps < 0 || c.CommissionRateBps > 10_000:
		return errors.New("COMMISSION_RATE_BPS must be between 0 and 10000")
	case c.PaymentTimeout <= 0:
		return errors.New("PAYMENT_TIMEOUT must be > 0")
	case c.AutoCompleteAfter <= 0:
		return errors.New("AUTO_COMPLETE_AFTER must be > 0")
	}
	return nil
}
