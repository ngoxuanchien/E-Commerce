package api_test

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/e2e/client"
)

func TestGatewayAuthBoundaries(t *testing.T) {
	w := getWorld(t)
	anon := client.New(gatewayURL, newTransport(t))

	_, err := anon.Me(ctx(t))
	assert.True(t, client.IsCode(err, "UNAUTHENTICATED"), "anonymous /api/users/me: %v", err)

	// A spoofed identity header without a token is stripped: still 401.
	err = anon.Do(ctx(t), http.MethodGet, "/api/cart", nil, nil, http.Header{"X-User-Id": {w.buyers[0].User.ID}})
	assert.True(t, client.IsCode(err, "UNAUTHENTICATED"), "spoofed X-User-Id: %v", err)

	// An invalid token fails loudly even on a public route (WS4 §3). The
	// contract does not document this 401 yet (finding F2), so this call
	// skips contract validation.
	bad := client.New(gatewayURL, client.RetryRateLimited{MaxWait: 3 * time.Minute})
	bad.SetToken("not-a-jwt")
	_, err = bad.SearchProducts(ctx(t), url.Values{})
	assert.True(t, client.IsCode(err, "UNAUTHENTICATED"), "garbage token on public route: %v", err)

	// A buyer is not a seller.
	_, err = w.buyers[0].SellerProducts(ctx(t), "")
	assert.True(t, client.IsCode(err, "FORBIDDEN"), "buyer on seller route: %v", err)

	me, err := w.buyers[0].Me(ctx(t))
	require.NoError(t, err)
	assert.Equal(t, w.buyers[0].User.ID, me.ID)
}

// Refresh rotation (WS1 §3): reusing a rotated refresh token revokes the
// whole family; logout ends the session.
func TestRefreshRotationReuseDetectionAndLogout(t *testing.T) {
	run := runID()
	a := newActor(t, "e2e-"+run+"-auth@example.test", "Người dùng Auth")
	refreshURL, _ := url.Parse(gatewayURL + "/api/auth/refresh")
	old := a.HTTP.Jar.Cookies(refreshURL)
	require.NotEmpty(t, old, "login set the refresh cookie")

	_, err := a.Refresh(ctx(t))
	require.NoError(t, err)

	thief := client.New(gatewayURL, newTransport(t))
	thief.HTTP.Jar.SetCookies(refreshURL, old)
	_, err = thief.Refresh(ctx(t))
	assert.Equal(t, http.StatusUnauthorized, client.StatusOf(err), "rotated token is rejected")

	_, err = a.Refresh(ctx(t))
	assert.Equal(t, http.StatusUnauthorized, client.StatusOf(err), "reuse revoked the whole family")

	_, err = a.Login(ctx(t), a.Email, password)
	require.NoError(t, err)
	require.NoError(t, a.Logout(ctx(t)))
	_, err = a.Refresh(ctx(t))
	assert.Equal(t, http.StatusUnauthorized, client.StatusOf(err), "logout revoked the session")
}
