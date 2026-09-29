package main

import (
	"errors"
	"time"

	"github.com/ngoxuanchien/e-commerce/pkg/config"
)

// Config is the service configuration (WS0 base + WS3 §6 variables).
type Config struct {
	config.Base
	CatalogGRPCAddr string        `env:"CATALOG_GRPC_ADDR,required,notEmpty"`
	ShippingFeeFlat int64         `env:"SHIPPING_FEE_FLAT" envDefault:"30000"`
	SweeperInterval time.Duration `env:"SWEEPER_INTERVAL" envDefault:"60s"`
	PendingTimeout  time.Duration `env:"PENDING_TIMEOUT" envDefault:"2m"`
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
	return nil
}
