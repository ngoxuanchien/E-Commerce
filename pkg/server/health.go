// Package server runs a service's HTTP and gRPC servers with graceful
// shutdown and provides the /healthz and /readyz handlers.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
)

// ReadyTimeout bounds how long all readiness checks together may take.
const ReadyTimeout = 2 * time.Second

// Check reports whether a dependency is usable.
type Check func(ctx context.Context) error

// Healthz is the liveness handler: 200 while the process is running.
func Healthz() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
}

type readyBody struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

// Readyz runs every check concurrently and responds 200 when all pass, 503 otherwise.
func Readyz(checks map[string]Check) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), ReadyTimeout)
		defer cancel()

		body := readyBody{Status: "ok", Checks: make(map[string]string, len(checks))}
		var mu sync.Mutex
		var wg sync.WaitGroup
		for name, check := range checks {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := check(ctx)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					slog.WarnContext(r.Context(), "readiness check failed", "check", name, "error", err)
					body.Checks[name] = "unavailable"
					body.Status = "unavailable"
					return
				}
				body.Checks[name] = "ok"
			}()
		}
		wg.Wait()

		status := http.StatusOK
		if body.Status != "ok" {
			status = http.StatusServiceUnavailable
		}
		httpx.WriteJSON(w, status, body)
	})
}

// ReadyURL turns a listen address such as ":8081" into the local /readyz URL.
func ReadyURL(httpAddr string) string {
	if httpAddr == "" {
		httpAddr = ":8080"
	}
	_, port, err := net.SplitHostPort(httpAddr)
	if err != nil {
		port = "8080"
	}
	return "http://127.0.0.1:" + port + "/readyz"
}

// Probe GETs url and returns nil on a 2xx response.
func Probe(url string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("probe %s: status %d", url, resp.StatusCode)
	}
	return nil
}

// HealthcheckCommand implements `server healthcheck` for distroless container
// healthchecks: it probes the local /readyz and returns the process exit code.
func HealthcheckCommand(httpAddr string) int {
	if err := Probe(ReadyURL(httpAddr)); err != nil {
		fmt.Println(err)
		return 1
	}
	return 0
}
