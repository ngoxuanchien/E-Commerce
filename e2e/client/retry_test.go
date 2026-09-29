package client_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/e2e/client"
)

func TestRetryRateLimitedWaitsAndResendsBody(t *testing.T) {
	var calls atomic.Int32
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	hc := &http.Client{Transport: client.RetryRateLimited{MaxWait: 5 * time.Second}}
	start := time.Now()
	resp, err := hc.Post(srv.URL, "application/json", strings.NewReader(`{"a":1}`))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int32(2), calls.Load())
	assert.Equal(t, []string{`{"a":1}`, `{"a":1}`}, bodies)
	assert.GreaterOrEqual(t, time.Since(start), time.Second)
}

func TestRetryRateLimitedGivesUpAfterMaxWait(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	hc := &http.Client{Transport: client.RetryRateLimited{MaxWait: time.Second}}
	start := time.Now()
	resp, err := hc.Get(srv.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	assert.Less(t, time.Since(start), time.Second)
}
