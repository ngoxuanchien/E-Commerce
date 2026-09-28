// Package testutil starts throwaway infrastructure for integration tests
// (testcontainers) and validates HTTP traffic against the OpenAPI contracts.
// Container helpers skip the test under `go test -short`.
package testutil

import (
	"context"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/kafka"
	"github.com/testcontainers/testcontainers-go/modules/minio"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
)

const (
	PostgresImage = "postgres:17-alpine"
	RedisImage    = "redis:7-alpine"
	KafkaImage    = "confluentinc/confluent-local:7.9.10"
	MinIOImage    = "pgsty/minio:RELEASE.2026-08-04T00-00-00Z"
)

func skipShort(t testing.TB) {
	t.Helper()
	if testing.Short() {
		t.Skip("container test skipped in -short mode")
	}
}

// Postgres starts PostgreSQL and returns a DSN for database "test".
func Postgres(t testing.TB) string {
	skipShort(t)
	ctx := context.Background()
	c, err := postgres.Run(ctx, PostgresImage,
		postgres.WithDatabase("test"), postgres.WithUsername("test"), postgres.WithPassword("test"),
		postgres.BasicWaitStrategies())
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("postgres dsn: %v", err)
	}
	return dsn
}

// Redis starts Redis and returns its URL (redis://host:port).
func Redis(t testing.TB) string {
	skipShort(t)
	ctx := context.Background()
	c, err := redis.Run(ctx, RedisImage)
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("start redis: %v", err)
	}
	url, err := c.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("redis url: %v", err)
	}
	return url
}

// Kafka starts a single-node Kafka and returns its bootstrap brokers.
func Kafka(t testing.TB) []string {
	skipShort(t)
	ctx := context.Background()
	c, err := kafka.Run(ctx, KafkaImage, kafka.WithClusterID("test-cluster"))
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("start kafka: %v", err)
	}
	brokers, err := c.Brokers(ctx)
	if err != nil {
		t.Fatalf("kafka brokers: %v", err)
	}
	return brokers
}

// CreateTopic creates topic with the given partition count (replication factor 1).
func CreateTopic(t testing.TB, brokers []string, topic string, partitions int32) {
	t.Helper()
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		t.Fatalf("kafka client: %v", err)
	}
	defer cl.Close()
	resp, err := kadm.NewClient(cl).CreateTopic(context.Background(), partitions, 1, nil, topic)
	if err != nil {
		t.Fatalf("create topic %s: %v", topic, err)
	}
	if resp.Err != nil {
		t.Fatalf("create topic %s: %v", topic, resp.Err)
	}
}

// MinIOInfo describes a running MinIO.
type MinIOInfo struct {
	Endpoint  string // host:port
	AccessKey string
	SecretKey string
}

// MinIO starts MinIO and returns its endpoint and credentials.
func MinIO(t testing.TB) MinIOInfo {
	skipShort(t)
	ctx := context.Background()
	c, err := minio.Run(ctx, MinIOImage, minio.WithUsername("minioadmin"), minio.WithPassword("minioadmin"))
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("start minio: %v", err)
	}
	endpoint, err := c.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("minio endpoint: %v", err)
	}
	return MinIOInfo{Endpoint: endpoint, AccessKey: c.Username, SecretKey: c.Password}
}
