package bootstrap

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestClaimTimeoutOutlastsTheSweepOfADeadRun(t *testing.T) {
	cfg := Config{PendingTimeout: 2 * time.Minute, SweeperInterval: time.Minute}
	assert.Equal(t, 4*time.Minute, cfg.claimTimeout(), "PENDING_TIMEOUT + SWEEPER_INTERVAL + the pre-Tx A margin")
}
