package main

import "github.com/ngoxuanchien/e-commerce/pkg/config"

// Config is ledger-svc's configuration. WS2' adds the service-specific
// variables listed in WS0' §8 (already set in deploy/docker-compose.yml).
type Config struct {
	config.Base
}
