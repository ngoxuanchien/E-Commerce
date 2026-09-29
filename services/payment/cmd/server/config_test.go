package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/config"
)

func TestConfigRequiresServiceName(t *testing.T) {
	t.Setenv("SERVICE_NAME", "")
	_, err := config.Load[Config]()
	assert.Error(t, err)
	t.Setenv("SERVICE_NAME", "payment")
	cfg, err := config.Load[Config]()
	require.NoError(t, err)
	assert.Equal(t, "payment", cfg.ServiceName)
}
