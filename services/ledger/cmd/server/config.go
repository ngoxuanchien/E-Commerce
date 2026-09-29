package main

import (
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
