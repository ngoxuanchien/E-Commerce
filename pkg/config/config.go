// Package config loads service configuration from environment variables.
package config

import (
	"fmt"
	"reflect"
	"strings"
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
		return cfg, fmt.Errorf("load config: %w", enhanceParseError(err, cfg))
	}
	return cfg, nil
}

func enhanceParseError(err error, cfg interface{}) error {
	errMsg := err.Error()

	// Get the type of the config
	t := reflect.TypeOf(cfg)

	// Try to find which field had the error
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if strings.Contains(errMsg, field.Name) {
			// Found the field that had the error
			if envTag, ok := field.Tag.Lookup("env"); ok {
				parts := strings.Split(envTag, ",")
				envVar := parts[0]
				// Include env var name in error if not already there
				if !strings.Contains(errMsg, envVar) {
					return fmt.Errorf("%s (env: %s): %w", field.Name, envVar, err)
				}
			}
		}
	}

	return err
}
