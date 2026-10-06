package api_test

import (
	"os"
	"time"
)

// envDuration reads a duration from an environment variable, falling back to def.
func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return def
		}
		return d
	}
	return def
}

// Timer values for P2 e2e tests, with e2e defaults.
var (
	paymentTimeout       = envDuration("PAYMENT_TIMEOUT", 40*time.Second)
	sweeperInterval      = envDuration("SWEEPER_INTERVAL", 2*time.Second)
	autoCompleteAfter    = envDuration("AUTO_COMPLETE_AFTER", 5*time.Second)
	reconcileInterval    = envDuration("RECONCILE_INTERVAL", 2*time.Second)
	reconcileMinAge      = envDuration("RECONCILE_MIN_AGE", 3*time.Second)
	fakeWebhookDelay     = envDuration("FAKE_WEBHOOK_DELAY", 300*time.Millisecond)
	payoutSettleInterval = envDuration("PAYOUT_SETTLE_INTERVAL", 1*time.Second)
	payoutSettleDelay    = envDuration("PAYOUT_SETTLE_DELAY", 2*time.Second)
)

const refundInterval = 10 * time.Second // payment-svc refund worker period; not configurable in compose
