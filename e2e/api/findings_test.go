package api_test

import (
	"fmt"
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

// F1: through the web container every browser shares nginx's IP, so eleven
// logins from eleven different clients exhaust one 10/min bucket.
func TestBrowserClientsHaveSeparateLoginBuckets(t *testing.T) {
	requireFindings(t, "F1")
	webURL := envOr("WEB_URL", "http://localhost:3000")
	limited := 0
	for i := range 11 {
		req, _ := http.NewRequestWithContext(ctx(t), http.MethodPost, webURL+"/api/auth/login",
			strings.NewReader(`{"email":"nobody@example.test","password":"matkhau123"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i+1)) // TEST-NET-3: eleven distinct clients
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			limited++
		}
	}
	assert.Zero(t, limited, "distinct clients behind the web proxy must not share a login bucket")
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
