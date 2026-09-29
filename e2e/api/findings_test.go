package api_test

import (
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ngoxuanchien/e-commerce/e2e/client"
)

// Tests in this file reproduce bugs reported to their owning workstream (see
// the Findings table in docs/superpowers/plans/2026-09-28-ws6-integration.md).
// They fail until the owner fixes the bug, so they only run with E2E_FINDINGS=1.
func requireFindings(t *testing.T, id string) {
	t.Helper()
	if os.Getenv("E2E_FINDINGS") != "1" {
		t.Skipf("finding %s: set E2E_FINDINGS=1 to run the reproduction", id)
	}
}

// F1: the gateway keys rate limits on its TCP peer and trusts no proxy, so
// requests arriving through the web container's nginx are counted against
// nginx's address instead of the real client. The same client therefore
// gets a second, separate login bucket through :3000, and every browser user
// shares that one bucket. Once the gateway trusts the web proxy, a client
// that drained its login bucket on :8080 is also limited through :3000.
func TestLoginsThroughWebProxyShareTheClientBucket(t *testing.T) {
	requireFindings(t, "F1")
	webURL := envOr("WEB_URL", "http://localhost:3000")
	login := func(base string) int {
		req, _ := http.NewRequestWithContext(ctx(t), http.MethodPost, base+"/api/auth/login",
			strings.NewReader(`{"email":"nobody@example.test","password":"matkhau123"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	drained := false
	for range 12 { // burst is 10
		if login(gatewayURL) == http.StatusTooManyRequests {
			drained = true
			break
		}
	}
	if !drained {
		t.Fatal("could not drain the login bucket on the gateway")
	}
	assert.Equal(t, http.StatusTooManyRequests, login(webURL),
		"the same client must share its login bucket when it comes through the web proxy")
}

// F2: the gateway answers 401 UNAUTHENTICATED to an invalid bearer token on
// public routes (WS4 §3), but the public operations in api/openapi do not
// document a 401 response, so the contract check rejects the real answer.
func TestPublicRoutesDocumentInvalidToken401(t *testing.T) {
	requireFindings(t, "F2")
	bad := client.New(gatewayURL, newTransport(t))
	bad.SetToken("not-a-jwt")
	_, err := bad.SearchProducts(ctx(t), url.Values{})
	assert.True(t, client.IsCode(err, "UNAUTHENTICATED"), "want a contract-valid 401, got: %v", err)
}
