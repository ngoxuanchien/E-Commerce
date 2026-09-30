package main

import (
	"errors"
	"time"

	"github.com/ngoxuanchien/e-commerce/pkg/config"
)

// Config is ledger-svc's configuration (WS0' §8; set in deploy/docker-compose.yml).
type Config struct {
	config.Base
	PayoutMinAmount        int64         `env:"PAYOUT_MIN_AMOUNT" envDefault:"50000"`
	PayoutSettleInterval   time.Duration `env:"PAYOUT_SETTLE_INTERVAL" envDefault:"30s"`
	PayoutSettleDelay      time.Duration `env:"PAYOUT_SETTLE_DELAY" envDefault:"1m"`
	PendingPostingInterval time.Duration `env:"PENDING_POSTING_INTERVAL" envDefault:"10s"`
}

// validate rejects settings that would make the service misbehave at runtime.
func (c Config) validate() error {
	var errs []error
	if c.PayoutMinAmount < 1 {
		errs = append(errs, errors.New("PAYOUT_MIN_AMOUNT must be >= 1"))
	}
	if c.PayoutSettleInterval <= 0 {
		errs = append(errs, errors.New("PAYOUT_SETTLE_INTERVAL must be > 0"))
	}
	if c.PayoutSettleDelay < 0 {
		errs = append(errs, errors.New("PAYOUT_SETTLE_DELAY must be >= 0"))
	}
	if c.PendingPostingInterval <= 0 {
		errs = append(errs, errors.New("PENDING_POSTING_INTERVAL must be > 0"))
	}
	return errors.Join(errs...)
}
