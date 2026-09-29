// Command seed fills a running stack with demo data through the public
// gateway API (WS6 §2). It is idempotent: re-running only creates what is
// missing. Run from e2e/: go run ./cmd/seed.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/ngoxuanchien/e-commerce/e2e/client"
)

func main() {
	gateway := flag.String("gateway", envOr("GATEWAY_URL", "http://localhost:8080"), "gateway base URL")
	fixtures := flag.String("fixtures", "fixtures/images", "directory with product images")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	s := &seeder{
		gateway:  *gateway,
		fixtures: *fixtures,
		rt:       client.RetryRateLimited{MaxWait: 3 * time.Minute},
		upload:   &http.Client{Timeout: 30 * time.Second},
		out:      os.Stdout,
	}
	if err := s.run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
