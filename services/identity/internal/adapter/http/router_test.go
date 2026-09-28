package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/ngoxuanchien/e-commerce/pkg/authctx"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
	httpapi "github.com/ngoxuanchien/e-commerce/services/identity/internal/adapter/http"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/adapter/postgres"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/testsupport"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/token"
)

const specPath = "../../../../../api/openapi/identity.yaml"

type env struct {
	url      string
	contract *http.Client // validates every request/response against identity.yaml
	plain    *http.Client // for requests that deliberately violate the contract
	jwks     token.JWKS
}

func newEnv(t *testing.T) *env {
	t.Helper()
	store := postgres.NewStore(testsupport.Pool(t))
	signer := token.NewSigner(testsupport.RSAKey(t), 15*time.Minute)
	auth, err := app.NewAuthService(store, signer, app.AuthConfig{RefreshTTL: 720 * time.Hour, BcryptCost: bcrypt.MinCost})
	require.NoError(t, err)
	router := httpapi.NewRouter(auth, signer.JWKS(), httpapi.Config{CookieSecure: true, RefreshTTL: 720 * time.Hour})
	srv := httptest.NewServer(authctx.Middleware(router))
	t.Cleanup(srv.Close)
	return &env{
		url:      srv.URL,
		contract: &http.Client{Transport: testutil.ContractTransport(t, specPath, nil)},
		plain:    &http.Client{},
		jwks:     signer.JWKS(),
	}
}

func withCookie(value string) http.Header {
	return http.Header{"Cookie": {"refresh_token=" + value}}
}

type userBody struct {
	ID        string   `json:"id"`
	Email     string   `json:"email"`
	FullName  string   `json:"full_name"`
	Roles     []string `json:"roles"`
	CreatedAt string   `json:"created_at"`
}

type tokenBody struct {
	AccessToken string   `json:"access_token"`
	TokenType   string   `json:"token_type"`
	ExpiresIn   int      `json:"expires_in"`
	User        userBody `json:"user"`
}

func assertCleared(t *testing.T, resp *testsupport.Response) {
	t.Helper()
	c := testsupport.Cookie(resp, "refresh_token")
	require.NotNil(t, c, "Set-Cookie clears refresh_token")
	assert.Empty(t, c.Value)
	assert.Equal(t, -1, c.MaxAge, "Max-Age=0")
	assert.Equal(t, "/api/auth", c.Path)
}

func TestAuthFlow(t *testing.T) {
	e := newEnv(t)
	email := testsupport.Email()
	password := "correct horse battery"

	// Register (normalizes the email, never returns the hash).
	resp, body := testsupport.Call(t, e.contract, http.MethodPost, e.url+"/api/auth/register",
		map[string]string{"email": strings.ToUpper(email), "password": password, "full_name": "Alice"}, nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(body))
	assert.NotContains(t, string(body), "password")
	var user userBody
	require.NoError(t, json.Unmarshal(body, &user))
	assert.Equal(t, email, user.Email)
	assert.Equal(t, []string{"buyer"}, user.Roles)
	assert.Nil(t, testsupport.Cookie(resp, "refresh_token"), "registration does not log in")

	// Duplicate registration.
	resp, body = testsupport.Call(t, e.contract, http.MethodPost, e.url+"/api/auth/register",
		map[string]string{"email": email, "password": password, "full_name": "Alice"}, nil)
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Equal(t, "CONFLICT", testsupport.ErrorCode(t, body))

	// Login.
	resp, body = testsupport.Call(t, e.contract, http.MethodPost, e.url+"/api/auth/login",
		map[string]string{"email": email, "password": password}, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
	var tok tokenBody
	require.NoError(t, json.Unmarshal(body, &tok))
	assert.Equal(t, "Bearer", tok.TokenType)
	assert.Equal(t, 900, tok.ExpiresIn)
	assert.Equal(t, user.ID, tok.User.ID)
	claims := testsupport.ParseAccessToken(t, tok.AccessToken, e.jwks)
	assert.Equal(t, user.ID, claims.Subject)

	cookie := testsupport.Cookie(resp, "refresh_token")
	require.NotNil(t, cookie)
	assert.True(t, cookie.HttpOnly)
	assert.True(t, cookie.Secure)
	assert.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
	assert.Equal(t, "/api/auth", cookie.Path)
	assert.Equal(t, 2592000, cookie.MaxAge)
	first := cookie.Value

	// Me.
	resp, body = testsupport.Call(t, e.contract, http.MethodGet, e.url+"/api/users/me", nil,
		http.Header{authctx.HeaderUserID: {user.ID}})
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	var me userBody
	require.NoError(t, json.Unmarshal(body, &me))
	assert.Equal(t, user, me)

	// Refresh rotates the cookie.
	resp, body = testsupport.Call(t, e.contract, http.MethodPost, e.url+"/api/auth/refresh", nil, withCookie(first))
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	second := testsupport.Cookie(resp, "refresh_token").Value
	assert.NotEqual(t, first, second)

	// Reusing the old cookie is rejected and clears the cookie ...
	resp, body = testsupport.Call(t, e.contract, http.MethodPost, e.url+"/api/auth/refresh", nil, withCookie(first))
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, "UNAUTHENTICATED", testsupport.ErrorCode(t, body))
	assertCleared(t, resp)

	// ... and revokes the whole family, including the newest token.
	resp, _ = testsupport.Call(t, e.contract, http.MethodPost, e.url+"/api/auth/refresh", nil, withCookie(second))
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	// Logout revokes a fresh session.
	resp, body = testsupport.Call(t, e.contract, http.MethodPost, e.url+"/api/auth/login",
		map[string]string{"email": email, "password": password}, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	fresh := testsupport.Cookie(resp, "refresh_token").Value

	resp, _ = testsupport.Call(t, e.contract, http.MethodPost, e.url+"/api/auth/logout", nil, withCookie(fresh))
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	assertCleared(t, resp)

	resp, _ = testsupport.Call(t, e.contract, http.MethodPost, e.url+"/api/auth/refresh", nil, withCookie(fresh))
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestErrorsAndEdgeCases(t *testing.T) {
	e := newEnv(t)

	t.Run("register validation errors list fields", func(t *testing.T) {
		resp, body := testsupport.Call(t, e.plain, http.MethodPost, e.url+"/api/auth/register",
			map[string]string{"email": "nope", "password": "short", "full_name": ""}, nil)
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
		var eb struct {
			Error struct {
				Code    string `json:"code"`
				Details struct {
					Fields map[string]string `json:"fields"`
				} `json:"details"`
			} `json:"error"`
		}
		require.NoError(t, json.Unmarshal(body, &eb))
		assert.Equal(t, "VALIDATION_ERROR", eb.Error.Code)
		assert.Equal(t, map[string]string{
			"email": "must be a valid email", "password": "must be at least 8 characters", "full_name": "is required",
		}, eb.Error.Details.Fields)
	})

	t.Run("multibyte password over 72 bytes is rejected", func(t *testing.T) {
		resp, body := testsupport.Call(t, e.contract, http.MethodPost, e.url+"/api/auth/register",
			map[string]string{"email": testsupport.Email(), "password": strings.Repeat("ệ", 25), "full_name": "A"}, nil)
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Contains(t, string(body), "must be at most 72 bytes")
	})

	t.Run("unknown fields and bad JSON are validation errors", func(t *testing.T) {
		resp, body := testsupport.Call(t, e.plain, http.MethodPost, e.url+"/api/auth/register",
			`{"email":"a@example.com","password":"password1","full_name":"A","role":"admin"}`, nil)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Equal(t, "VALIDATION_ERROR", testsupport.ErrorCode(t, body))
		resp, _ = testsupport.Call(t, e.plain, http.MethodPost, e.url+"/api/auth/login", `{`, nil)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("wrong password and unknown email return identical 401s", func(t *testing.T) {
		email := testsupport.Email()
		resp, _ := testsupport.Call(t, e.contract, http.MethodPost, e.url+"/api/auth/register",
			map[string]string{"email": email, "password": "password1", "full_name": "A"}, nil)
		require.Equal(t, http.StatusCreated, resp.StatusCode)

		r1, b1 := testsupport.Call(t, e.contract, http.MethodPost, e.url+"/api/auth/login",
			map[string]string{"email": email, "password": "wrong-password"}, nil)
		r2, b2 := testsupport.Call(t, e.contract, http.MethodPost, e.url+"/api/auth/login",
			map[string]string{"email": testsupport.Email(), "password": "wrong-password"}, nil)
		assert.Equal(t, http.StatusUnauthorized, r1.StatusCode)
		assert.Equal(t, http.StatusUnauthorized, r2.StatusCode)
		assert.Equal(t, string(b1), string(b2))
		assert.Nil(t, testsupport.Cookie(r1, "refresh_token"))
	})

	t.Run("refresh without or with a garbage cookie is 401 and clears it", func(t *testing.T) {
		for name, header := range map[string]http.Header{"missing": nil, "garbage": withCookie("not-a-real-token")} {
			resp, body := testsupport.Call(t, e.contract, http.MethodPost, e.url+"/api/auth/refresh", nil, header)
			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, name)
			assert.Equal(t, "UNAUTHENTICATED", testsupport.ErrorCode(t, body), name)
			assertCleared(t, resp)
		}
	})

	t.Run("logout without or with a garbage cookie is 204", func(t *testing.T) {
		for name, header := range map[string]http.Header{"missing": nil, "garbage": withCookie("not-a-real-token")} {
			resp, _ := testsupport.Call(t, e.contract, http.MethodPost, e.url+"/api/auth/logout", nil, header)
			assert.Equal(t, http.StatusNoContent, resp.StatusCode, name)
			assertCleared(t, resp)
		}
	})

	t.Run("me rejects anonymous, malformed and unknown identities", func(t *testing.T) {
		for name, header := range map[string]http.Header{
			"anonymous": nil,
			"malformed": {authctx.HeaderUserID: {"not-a-uuid"}},
			"unknown":   {authctx.HeaderUserID: {uuid.Must(uuid.NewV7()).String()}},
		} {
			resp, body := testsupport.Call(t, e.contract, http.MethodGet, e.url+"/api/users/me", nil, header)
			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, name)
			assert.Equal(t, "UNAUTHENTICATED", testsupport.ErrorCode(t, body), name)
		}
	})

	t.Run("jwks publishes the signing key", func(t *testing.T) {
		resp, body := testsupport.Call(t, e.contract, http.MethodGet, e.url+"/.well-known/jwks.json", nil, nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var got token.JWKS
		require.NoError(t, json.Unmarshal(body, &got))
		assert.Equal(t, e.jwks, got)
	})

	t.Run("unknown route is a JSON 404", func(t *testing.T) {
		resp, body := testsupport.Call(t, e.plain, http.MethodGet, e.url+"/api/nope", nil, nil)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		assert.Equal(t, "NOT_FOUND", testsupport.ErrorCode(t, body))
	})
}
