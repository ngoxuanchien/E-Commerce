package api_test

import (
	"net/url"
	"os"
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
