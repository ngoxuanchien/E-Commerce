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
	t.Setenv("SERVICE_NAME", "ledger")
	cfg, err := config.Load[Config]()
	require.NoError(t, err)
	assert.Equal(t, "ledger", cfg.ServiceName)
}
func TestConfigLedgerDefaultsAndOverrides(t *testing.T) {
	t.Setenv("SERVICE_NAME", "ledger")
	cfg, err := config.Load[Config]()
	require.NoError(t, err)
	assert.Equal(t, int64(50000), cfg.PayoutMinAmount)
	assert.Equal(t, 30*time.Second, cfg.PayoutSettleInterval)
	assert.Equal(t, time.Minute, cfg.PayoutSettleDelay)
	assert.Equal(t, 10*time.Second, cfg.PendingPostingInterval)

	t.Setenv("PAYOUT_MIN_AMOUNT", "1000")
	t.Setenv("PAYOUT_SETTLE_DELAY", "5s")
	cfg, err = config.Load[Config]()
	require.NoError(t, err)
	assert.Equal(t, int64(1000), cfg.PayoutMinAmount)
	assert.Equal(t, 5*time.Second, cfg.PayoutSettleDelay)
}
