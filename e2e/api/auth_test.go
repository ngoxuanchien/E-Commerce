package api_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/e2e/client"
)

func TestGatewayAuthBoundaries(t *testing.T) {
	w := getWorld(t)
	anon := client.New(gatewayURL, newTransport(t))

	_, err := anon.Me(ctx(t))
	assert.True(t, client.IsCode(err, "UNAUTHENTICATED"), "anonymous /api/users/me: %v", err)

	// Spoofed identity headers next to a valid token are stripped: the
	// gateway injects the token's identity, so the buyer stays the buyer.
	spoof := http.Header{"X-User-Id": {w.sellerA.User.ID}, "X-User-Roles": {"buyer,seller"}}
	var me client.User
	require.NoError(t, w.buyers[0].Do(ctx(t), http.MethodGet, "/api/users/me", nil, &me, spoof))
	assert.Equal(t, w.buyers[0].User.ID, me.ID, "X-User-Id from the client must not override the token")
	err = w.buyers[0].Do(ctx(t), http.MethodGet, "/api/seller/products", nil, nil, spoof)
	assert.True(t, client.IsCode(err, "FORBIDDEN"), "X-User-Roles from the client must not grant seller: %v", err)

	// An invalid token fails loudly even on a public route (WS4 §3), and the
	// contract documents that 401 (former finding F2).
	bad := client.New(gatewayURL, newTransport(t))
	bad.SetToken("not-a-jwt")
	_, err = bad.SearchProducts(ctx(t), url.Values{})
	assert.True(t, client.IsCode(err, "UNAUTHENTICATED"), "garbage token on public route: %v", err)

	// A buyer is not a seller.
	_, err = w.buyers[0].SellerProducts(ctx(t), "")
	assert.True(t, client.IsCode(err, "FORBIDDEN"), "buyer on seller route: %v", err)

	me, err = w.buyers[0].Me(ctx(t))
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
