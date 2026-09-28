package server_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/ngoxuanchien/e-commerce/pkg/server"
)

func get(h http.Handler) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	return rec
}

func TestHealthz(t *testing.T) {
	rec := get(server.Healthz())
	assert.Equal(t, 200, rec.Code)
	assert.JSONEq(t, `{"status":"ok"}`, rec.Body.String())
}

func TestReadyzAllOK(t *testing.T) {
	ok := func(context.Context) error { return nil }
	rec := get(server.Readyz(map[string]server.Check{"postgres": ok, "kafka": ok}))
	assert.Equal(t, 200, rec.Code)
	assert.JSONEq(t, `{"status":"ok","checks":{"postgres":"ok","kafka":"ok"}}`, rec.Body.String())
}

func TestReadyzNoChecks(t *testing.T) {
	rec := get(server.Readyz(nil))
	assert.Equal(t, 200, rec.Code)
	assert.JSONEq(t, `{"status":"ok","checks":{}}`, rec.Body.String())
}

func TestReadyzFailingCheck(t *testing.T) {
	rec := get(server.Readyz(map[string]server.Check{
		"postgres": func(context.Context) error { return nil },
		"redis":    func(context.Context) error { return errors.New("dial tcp: refused") },
	}))
	assert.Equal(t, 503, rec.Code)
	assert.JSONEq(t, `{"status":"unavailable","checks":{"postgres":"ok","redis":"unavailable"}}`, rec.Body.String())
}

func TestReadyzHangingCheckTimesOut(t *testing.T) {
	start := time.Now()
	rec := get(server.Readyz(map[string]server.Check{
		"slow": func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
	}))
	assert.Equal(t, 503, rec.Code)
	assert.Less(t, time.Since(start), server.ReadyTimeout+time.Second)
}

func TestReadyURL(t *testing.T) {
	assert.Equal(t, "http://127.0.0.1:8081/readyz", server.ReadyURL(":8081"))
	assert.Equal(t, "http://127.0.0.1:8080/readyz", server.ReadyURL(""))
	assert.Equal(t, "http://127.0.0.1:9000/readyz", server.ReadyURL("0.0.0.0:9000"))
}

func TestProbe(t *testing.T) {
	up := httptest.NewServer(server.Healthz())
	defer up.Close()
	assert.NoError(t, server.Probe(up.URL))

	down := httptest.NewServer(server.Readyz(map[string]server.Check{"x": func(context.Context) error { return errors.New("no") }}))
	defer down.Close()
	assert.Error(t, server.Probe(down.URL))

	assert.Error(t, server.Probe("http://127.0.0.1:1/readyz"))
}
