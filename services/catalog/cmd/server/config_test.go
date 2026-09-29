package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/config"
)

func setBaseEnv(t *testing.T) {
	t.Setenv("SERVICE_NAME", "catalog")
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/catalog_db?sslmode=disable")
	t.Setenv("KAFKA_BROKERS", "kafka:9092")
	t.Setenv("IDENTITY_GRPC_ADDR", "identity:9081")
	t.Setenv("MINIO_ENDPOINT", "minio:9000")
	t.Setenv("MINIO_ACCESS_KEY", "minioadmin")
	t.Setenv("MINIO_SECRET_KEY", "minioadmin")
	t.Setenv("PUBLIC_ASSET_BASE_URL", "http://localhost:9000/product-images")
}

func TestConfigLoadsCatalogVars(t *testing.T) {
	setBaseEnv(t)
	cfg, err := config.Load[Config]()
	require.NoError(t, err)
	assert.Equal(t, "identity:9081", cfg.IdentityGRPCAddr)
	assert.Equal(t, "product-images", cfg.MinIOBucket, "default bucket")
	require.NoError(t, cfg.validate())
}

func TestConfigRequiresCatalogVars(t *testing.T) {
	for _, name := range []string{"IDENTITY_GRPC_ADDR", "MINIO_ENDPOINT", "MINIO_ACCESS_KEY", "MINIO_SECRET_KEY", "PUBLIC_ASSET_BASE_URL"} {
		t.Run(name, func(t *testing.T) {
			setBaseEnv(t)
			t.Setenv(name, "")
			_, err := config.Load[Config]()
			require.Error(t, err)
			assert.Contains(t, err.Error(), name)
		})
	}
}

func TestConfigValidateRequiresKafkaAndDatabase(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("KAFKA_BROKERS", "")
	cfg, err := config.Load[Config]()
	require.NoError(t, err)
	assert.ErrorContains(t, cfg.validate(), "KAFKA_BROKERS")
}
