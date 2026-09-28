package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	identityv1 "github.com/ngoxuanchien/e-commerce/gen/identity/v1"
	"github.com/ngoxuanchien/e-commerce/pkg/grpcx"
	"github.com/ngoxuanchien/e-commerce/pkg/server"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/testsupport"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/token"
)

const specPath = "../../../../api/openapi/identity.yaml"

// TestServiceWiring drives the fully wired service (middleware, REST, gRPC)
// and checks that no secret ever reaches the logs.
func TestServiceWiring(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	prev := slog.Default()
	slog.SetDefault(logger) // httpx.WriteError logs through the default logger
	t.Cleanup(func() { slog.SetDefault(prev) })

	pool := testsupport.Pool(t)
	cfg := Config{AccessTokenTTL: 15 * time.Minute, RefreshTokenTTL: 720 * time.Hour, CookieSecure: false}
	comps, err := newComponents(pool, testsupport.RSAKey(t), cfg, bcrypt.MinCost, logger)
	require.NoError(t, err)

	srv := httptest.NewServer(newHandler(logger, map[string]server.Check{"postgres": pool.Ping}, comps.API))
	t.Cleanup(srv.Close)
	client := &http.Client{Transport: testutil.ContractTransport(t, specPath, nil)}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	gs, _ := grpcx.NewServer()
	identityv1.RegisterIdentityInternalServer(gs, comps.GRPC)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	conn, err := grpcx.Dial(lis.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	grpcClient := identityv1.NewIdentityInternalClient(conn)

	resp, _ := testsupport.Call(t, &http.Client{}, http.MethodGet, srv.URL+"/readyz", nil, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	email, password := testsupport.Email(), "super secret password"
	resp, body := testsupport.Call(t, client, http.MethodPost, srv.URL+"/api/auth/register",
		map[string]string{"email": email, "password": password, "full_name": "Seller To Be"}, nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(body))
	var user struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(body, &user))

	resp, body = testsupport.Call(t, client, http.MethodPost, srv.URL+"/api/auth/login",
		map[string]string{"email": email, "password": password}, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	cookie := testsupport.Cookie(resp, "refresh_token")
	require.NotNil(t, cookie)
	assert.False(t, cookie.Secure, "COOKIE_SECURE=false in dev")
	var login struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.Unmarshal(body, &login))

	// catalog-svc grants seller (twice: idempotent) ...
	for range 2 {
		_, err = grpcClient.GrantRole(context.Background(), &identityv1.GrantRoleRequest{UserId: user.ID, Role: "seller"})
		require.NoError(t, err)
	}

	// ... and the next refresh carries the new role, verifiable with the served JWKS.
	resp, body = testsupport.Call(t, client, http.MethodPost, srv.URL+"/api/auth/refresh", nil,
		http.Header{"Cookie": {"refresh_token=" + cookie.Value}})
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	var refreshed struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.Unmarshal(body, &refreshed))

	resp, body = testsupport.Call(t, client, http.MethodGet, srv.URL+"/.well-known/jwks.json", nil, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var jwks token.JWKS
	require.NoError(t, json.Unmarshal(body, &jwks))
	claims := testsupport.ParseAccessToken(t, refreshed.AccessToken, jwks)
	assert.Equal(t, []string{"buyer", "seller"}, claims.Roles)

	// Reuse of the rotated cookie triggers the warning log.
	resp, _ = testsupport.Call(t, client, http.MethodPost, srv.URL+"/api/auth/refresh", nil,
		http.Header{"Cookie": {"refresh_token=" + cookie.Value}})
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	out := logs.String()
	require.NotEmpty(t, out)
	assert.Contains(t, out, "refresh token reuse detected")
	for name, secret := range map[string]string{
		"password": password, "refresh token": cookie.Value,
		"login access token": login.AccessToken, "refreshed access token": refreshed.AccessToken,
	} {
		assert.NotContains(t, out, secret, "%s leaked into logs", name)
	}
	assert.NotContains(t, out, "password_hash")
	assert.NotContains(t, out, "$2a$", "no bcrypt hash in logs")
}
