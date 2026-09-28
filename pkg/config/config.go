// Package config loads service configuration from environment variables.
package config

import (
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"
)

// Base holds the variables every service understands (WS0 §3). Services embed
// it in their own config struct and add service-specific fields.
type Base struct {
	ServiceName     string        `env:"SERVICE_NAME,required,notEmpty"`
	HTTPAddr        string        `env:"HTTP_ADDR" envDefault:":8080"`
	GRPCAddr        string        `env:"GRPC_ADDR"`
	DatabaseURL     string        `env:"DATABASE_URL"`
	RedisURL        string        `env:"REDIS_URL"`
	KafkaBrokers    []string      `env:"KAFKA_BROKERS" envSeparator:","`
	OTLPEndpoint    string        `env:"OTEL_EXPORTER_OTLP_ENDPOINT"`
	LogLevel        string        `env:"LOG_LEVEL" envDefault:"info"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"15s"`
}

// Load parses the environment into a new T.
func Load[T any]() (T, error) {
	var cfg T
	if err := env.Parse(&cfg); err != nil {
		return cfg, fmt.Errorf("load config: %w", err)
	}
	return cfg, nil
}
