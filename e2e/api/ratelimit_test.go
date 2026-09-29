package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rawLogin posts a failing login to base and returns the status, bypassing
// the 429 retry so the tests can observe the limiter directly.
func rawLogin(t *testing.T, base string, hdr http.Header) int {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx(t), http.MethodPost, base+"/api/auth/login",
		strings.NewReader(`{"email":"nobody@example.test","password":"matkhau123"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	for k, vs := range hdr {
		req.Header[k] = vs
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	return resp.StatusCode
}

// drainLoginBucket spends this client's login tokens (burst 10) on the gateway.
func drainLoginBucket(t *testing.T) {
	t.Helper()
	for range 12 {
		if rawLogin(t, gatewayURL, nil) == http.StatusTooManyRequests {
			return
		}
	}
	t.Fatal("could not drain the login bucket on the gateway")
}

// The gateway trusts only the web container as a proxy, so a client keeps a
// single login bucket whether it calls the gateway directly or goes through
// the web app (former finding F1).
func TestLoginsThroughWebProxyShareTheClientBucket(t *testing.T) {
	drainLoginBucket(t)
	assert.Equal(t, http.StatusTooManyRequests, rawLogin(t, envOr("WEB_URL", "http://localhost:3000"), nil),
		"the same client must share its login bucket when it comes through the web proxy")
}

// A direct caller is not a trusted proxy, so a forged X-Forwarded-For must
// not buy it a fresh bucket.
func TestForgedForwardedForDoesNotBypassTheLoginLimit(t *testing.T) {
	drainLoginBucket(t)
	assert.Equal(t, http.StatusTooManyRequests, rawLogin(t, gatewayURL, http.Header{"X-Forwarded-For": {"203.0.113.77"}}),
		"X-Forwarded-For from an untrusted peer must be ignored")
}
