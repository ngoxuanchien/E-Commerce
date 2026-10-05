// Package api_test holds the API smoke tests: the P1 flows run against the
// real stack through the gateway, and every call is checked against the
// OpenAPI contract.
package api_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"
)

var (
	gatewayURL = envOr("GATEWAY_URL", "http://localhost:8080")
	jaegerURL  = envOr("JAEGER_URL", "http://localhost:16686")
)

func TestMain(m *testing.M) {
	if err := waitReady(gatewayURL+"/readyz", 90*time.Second); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	out, err := trialBalance(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, "trial balance after suite:", out)
		os.Exit(1)
	}
	os.Exit(code)
}

func waitReady(url string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("gateway not ready at %s — start the stack with `make up-app`", url)
		case <-time.After(time.Second):
		}
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
