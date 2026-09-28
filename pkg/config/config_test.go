package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/config"
)

type svcConfig struct {
	config.Base
	PendingTimeout time.Duration `env:"PENDING_TIMEOUT" envDefault:"2m"`
}

func TestLoadParsesBaseAndServiceFields(t *testing.T) {
	t.Setenv("SERVICE_NAME", "order")
	t.Setenv("HTTP_ADDR", ":8083")
	t.Setenv("KAFKA_BROKERS", "k1:9092,k2:9092")
	t.Setenv("PENDING_TIMEOUT", "30s")

	cfg, err := config.Load[svcConfig]()
	require.NoError(t, err)

	assert.Equal(t, "order", cfg.ServiceName)
	assert.Equal(t, ":8083", cfg.HTTPAddr)
	assert.Equal(t, []string{"k1:9092", "k2:9092"}, cfg.KafkaBrokers)
	assert.Equal(t, 30*time.Second, cfg.PendingTimeout)
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("SERVICE_NAME", "gateway")

	cfg, err := config.Load[svcConfig]()
	require.NoError(t, err)

	assert.Equal(t, ":8080", cfg.HTTPAddr)
	assert.Equal(t, "info", cfg.LogLevel)
	assert.Equal(t, 15*time.Second, cfg.ShutdownTimeout)
	assert.Equal(t, 2*time.Minute, cfg.PendingTimeout)
	assert.Empty(t, cfg.KafkaBrokers)
}

func TestLoadRequiresServiceName(t *testing.T) {
	t.Setenv("SERVICE_NAME", "")
	_, err := config.Load[svcConfig]()
	assert.ErrorContains(t, err, "SERVICE_NAME")
}

func TestLoadRejectsBadDuration(t *testing.T) {
	t.Setenv("SERVICE_NAME", "x")
	t.Setenv("PENDING_TIMEOUT", "soon")
	_, err := config.Load[svcConfig]()
	assert.ErrorContains(t, err, "PENDING_TIMEOUT")
}
