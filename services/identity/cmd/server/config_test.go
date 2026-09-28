package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/config"
)

func TestConfigDefaults(t *testing.T) {
	t.Setenv("SERVICE_NAME", "identity")
	t.Setenv("JWT_PRIVATE_KEY_PATH", "/run/secrets/jwt_private.pem")
	cfg, err := config.Load[Config]()
	require.NoError(t, err)
	assert.Equal(t, 15*time.Minute, cfg.AccessTokenTTL)
	assert.Equal(t, 720*time.Hour, cfg.RefreshTokenTTL)
	assert.True(t, cfg.CookieSecure)
}

func TestConfigCookieSecureCanBeDisabled(t *testing.T) {
	t.Setenv("SERVICE_NAME", "identity")
	t.Setenv("JWT_PRIVATE_KEY_PATH", "/k.pem")
	t.Setenv("COOKIE_SECURE", "false")
	cfg, err := config.Load[Config]()
	require.NoError(t, err)
	assert.False(t, cfg.CookieSecure)
}

func TestConfigRequiresKeyPath(t *testing.T) {
	t.Setenv("SERVICE_NAME", "identity")
	t.Setenv("JWT_PRIVATE_KEY_PATH", "")
	_, err := config.Load[Config]()
	assert.ErrorContains(t, err, "JWT_PRIVATE_KEY_PATH")
}
