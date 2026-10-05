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

func TestConfigValidate(t *testing.T) {
	t.Setenv("SERVICE_NAME", "ledger")
	valid, err := config.Load[Config]()
	require.NoError(t, err)
	require.NoError(t, valid.validate())

	zeroDelay := valid
	zeroDelay.PayoutSettleDelay = 0
	assert.NoError(t, zeroDelay.validate())

	for name, mutate := range map[string]func(*Config){
		"min amount 0":      func(c *Config) { c.PayoutMinAmount = 0 },
		"settle interval 0": func(c *Config) { c.PayoutSettleInterval = 0 },
		"negative delay":    func(c *Config) { c.PayoutSettleDelay = -time.Second },
		"pending interval":  func(c *Config) { c.PendingPostingInterval = 0 },
	} {
		c := valid
		mutate(&c)
		assert.Error(t, c.validate(), name)
	}
}
