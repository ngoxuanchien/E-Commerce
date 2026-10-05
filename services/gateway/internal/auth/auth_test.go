package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/authctx"
	"github.com/ngoxuanchien/e-commerce/services/gateway/internal/auth/authtest"
	"github.com/ngoxuanchien/e-commerce/services/gateway/internal/routes"
)

// TestMain silences the default slog logger: Middleware logs rejection
// reasons through slog.InfoContext on it, and those lines would otherwise
// clutter `go test -v` output for every negative test case.
func TestMain(m *testing.M) {
	slog.SetDefault(discardLogger())
	os.Exit(m.Run())
}

// newTestVerifier starts a JWKS server serving k and returns a Verifier
// backed by a *JWKS whose key set has already been loaded.
func newTestVerifier(t *testing.T, keys ...*authtest.Key) (*Verifier, *authtest.Server) {
	t.Helper()
	srv := authtest.NewServer(t, keys...)
	j := NewJWKS(srv.URL)
	require.NoError(t, j.Refresh(context.Background()))
	return NewVerifier(j), srv
}

func TestVerifyValidToken(t *testing.T) {
	k1 := authtest.NewKey(t, "k1")
	v, _ := newTestVerifier(t, k1)

	raw := k1.Sign(t, authtest.ValidClaims("u1", "buyer", "seller"))
	id, err := v.Verify(context.Background(), raw)
	require.NoError(t, err)
	assert.Equal(t, "u1", id.UserID)
	assert.Equal(t, []string{"buyer", "seller"}, id.Roles)
}

func TestVerifyAppliesLeeway(t *testing.T) {
	k1 := authtest.NewKey(t, "k1")
	v, _ := newTestVerifier(t, k1)

	now := time.Now()
	cases := map[string]authtest.Claims{
		"exp -20s (within leeway)": {
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    authtest.Issuer,
				Subject:   "u1",
				ExpiresAt: jwt.NewNumericDate(now.Add(-20 * time.Second)),
			},
		},
		"nbf +20s (within leeway)": {
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    authtest.Issuer,
				Subject:   "u1",
				ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
				NotBefore: jwt.NewNumericDate(now.Add(20 * time.Second)),
			},
		},
	}
	for name, claims := range cases {
		t.Run(name, func(t *testing.T) {
			raw := k1.Sign(t, claims)
			id, err := v.Verify(context.Background(), raw)
			require.NoError(t, err)
			assert.Equal(t, "u1", id.UserID)
		})
	}
}

// rawRSAToken builds a token bypassing authtest.Key, for cases that need
// control authtest doesn't expose (no kid header, alg none, HS256).
func rawRSAToken(t *testing.T, method jwt.SigningMethod, key any, kid string, claims jwt.Claims) string {
	t.Helper()
	token := jwt.NewWithClaims(method, claims)
	if kid != "" {
		token.Header["kid"] = kid
	}
	signed, err := token.SignedString(key)
	require.NoError(t, err)
	return signed
}

func TestVerifyRejectsInvalidTokens(t *testing.T) {
	k1 := authtest.NewKey(t, "k1")
	v, _ := newTestVerifier(t, k1)
	now := time.Now()

	validClaims := func() authtest.Claims {
		c := authtest.ValidClaims("u1", "buyer")
		return c
	}

	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	cases := map[string]string{
		"expired -1m": func() string {
			c := validClaims()
			c.ExpiresAt = jwt.NewNumericDate(now.Add(-1 * time.Minute))
			return k1.Sign(t, c)
		}(),
		"nbf +1m": func() string {
			c := validClaims()
			c.NotBefore = jwt.NewNumericDate(now.Add(1 * time.Minute))
			return k1.Sign(t, c)
		}(),
		"missing exp": func() string {
			c := validClaims()
			c.ExpiresAt = nil
			return k1.Sign(t, c)
		}(),
		"wrong iss": func() string {
			c := validClaims()
			c.Issuer = "not-identity"
			return k1.Sign(t, c)
		}(),
		"missing iss": func() string {
			c := validClaims()
			c.Issuer = ""
			return k1.Sign(t, c)
		}(),
		"missing sub": func() string {
			c := validClaims()
			c.Subject = ""
			return k1.Sign(t, c)
		}(),
		"role contains comma": func() string {
			c := validClaims()
			c.Roles = []string{"buyer,seller"}
			return k1.Sign(t, c)
		}(),
		"role contains a space": func() string {
			c := validClaims()
			c.Roles = []string{"sel ler"}
			return k1.Sign(t, c)
		}(),
		"empty role": func() string {
			c := validClaims()
			c.Roles = []string{""}
			return k1.Sign(t, c)
		}(),
		"role contains a tab": func() string {
			c := validClaims()
			c.Roles = []string{"sel\tler"}
			return k1.Sign(t, c)
		}(),
		"roles given as a string": func() string {
			claims := jwt.MapClaims{
				"iss":   authtest.Issuer,
				"sub":   "u1",
				"exp":   now.Add(15 * time.Minute).Unix(),
				"roles": "buyer",
			}
			return k1.Sign(t, claims)
		}(),
		"different key, same kid": func() string {
			// k1b shares kid "k1" with k1 but has its own private key; the
			// JWKS server only knows k1's public key, so this must fail.
			k1b := authtest.NewKey(t, "k1")
			return k1b.Sign(t, validClaims())
		}(),
		"unknown kid": func() string {
			k2 := authtest.NewKey(t, "k2")
			return k2.Sign(t, validClaims())
		}(),
		"missing kid": rawRSAToken(t, jwt.SigningMethodRS256, otherKey, "", validClaims()),
		"alg none":    rawRSAToken(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, "k1", validClaims()),
		"HS256 with kid k1": rawRSAToken(t, jwt.SigningMethodHS256, []byte("shared-secret"), "k1",
			validClaims()),
		"malformed": "not-a-jwt",
		"empty":     "",
	}

	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := v.Verify(context.Background(), raw)
			assert.Error(t, err)
		})
	}
}

func TestStripIdentityHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("X-User-Id", "spoofed")
	h.Set("X-User-Roles", "seller")
	h.Add("x-user-id", "also-spoofed")
	h.Set("X_User_Id", "underscore-spoofed")
	h.Set("x_user_roles", "underscore-spoofed-roles")
	h.Set("X-USER-ID", "weird-case-spoofed")
	h.Set("X-User-Agentish", "kept")
	h.Set("Authorization", "Bearer abc")
	h.Set("Content-Type", "application/json")

	StripIdentityHeaders(h)

	assert.Empty(t, h.Values("X-User-Id"))
	assert.Empty(t, h.Values("X-User-Roles"))
	assert.Empty(t, h.Values("x-user-id"))
	assert.Empty(t, h.Values("X_User_Id"))
	assert.Empty(t, h.Values("x_user_roles"))
	assert.Empty(t, h.Values("X-USER-ID"))
	assert.Equal(t, []string{"kept"}, h.Values("X-User-Agentish"))
	assert.Equal(t, []string{"Bearer abc"}, h.Values("Authorization"))
	assert.Equal(t, []string{"application/json"}, h.Values("Content-Type"))
}

func TestBearerToken(t *testing.T) {
	cases := []struct {
		name        string
		setup       func(h http.Header)
		wantToken   string
		wantPresent bool
		wantOK      bool
	}{
		{
			name:        "absent",
			setup:       func(h http.Header) {},
			wantPresent: false,
			wantOK:      false,
		},
		{
			name:        "Bearer abc",
			setup:       func(h http.Header) { h.Set("Authorization", "Bearer abc") },
			wantToken:   "abc",
			wantPresent: true,
			wantOK:      true,
		},
		{
			name:        "bearer abc (lowercase scheme)",
			setup:       func(h http.Header) { h.Set("Authorization", "bearer abc") },
			wantToken:   "abc",
			wantPresent: true,
			wantOK:      true,
		},
		{
			name:        "Basic scheme",
			setup:       func(h http.Header) { h.Set("Authorization", "Basic dXNlcjpwYXNz") },
			wantPresent: true,
			wantOK:      false,
		},
		{
			name:        "Bearer with empty token",
			setup:       func(h http.Header) { h.Set("Authorization", "Bearer ") },
			wantPresent: true,
			wantOK:      false,
		},
		{
			name:        "empty header value",
			setup:       func(h http.Header) { h.Set("Authorization", "") },
			wantPresent: true,
			wantOK:      false,
		},
		{
			name:        "scheme only, no token",
			setup:       func(h http.Header) { h.Set("Authorization", "Bearer") },
			wantPresent: true,
			wantOK:      false,
		},
		{
			name:        "token containing a space",
			setup:       func(h http.Header) { h.Set("Authorization", "Bearer abc def") },
			wantPresent: true,
			wantOK:      false,
		},
		{
			name: "two Authorization headers",
			setup: func(h http.Header) {
				h.Add("Authorization", "Bearer abc")
				h.Add("Authorization", "Bearer def")
			},
			wantPresent: true,
			wantOK:      false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			tc.setup(h)
			token, present, ok := bearerToken(h)
			assert.Equal(t, tc.wantToken, token)
			assert.Equal(t, tc.wantPresent, present)
			assert.Equal(t, tc.wantOK, ok)
		})
	}
}

// newRecordingHandler returns a handler that records whether it was called
// and the request it saw, for assertions on what Middleware forwards.
func newRecordingHandler() (http.Handler, *bool, **http.Request) {
	called := false
	var seen *http.Request
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		seen = r
		w.WriteHeader(http.StatusOK)
	})
	return h, &called, &seen
}

func TestMiddlewareLevels(t *testing.T) {
	k1 := authtest.NewKey(t, "k1")
	v, _ := newTestVerifier(t, k1)

	buyerToken := k1.Sign(t, authtest.ValidClaims("u-buyer", "buyer"))
	sellerToken := k1.Sign(t, authtest.ValidClaims("u-seller", "buyer", "seller"))

	t.Run("public anon passes without identity headers", func(t *testing.T) {
		next, called, seen := newRecordingHandler()
		h := Middleware(v, routes.Public)(next)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

		assert.Equal(t, http.StatusOK, rec.Code)
		require.True(t, *called)
		assert.Empty(t, (*seen).Header.Get(authctx.HeaderUserID))
		assert.Empty(t, (*seen).Header.Get(authctx.HeaderUserRoles))
		_, ok := authctx.FromContext((*seen).Context())
		assert.False(t, ok)
	})

	t.Run("user route anon is rejected", func(t *testing.T) {
		next, called, _ := newRecordingHandler()
		h := Middleware(v, routes.User)(next)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.False(t, *called)
	})

	t.Run("seller route anon is rejected", func(t *testing.T) {
		next, called, _ := newRecordingHandler()
		h := Middleware(v, routes.Seller)(next)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.False(t, *called)
	})

	t.Run("buyer on user route is allowed with identity", func(t *testing.T) {
		next, called, seen := newRecordingHandler()
		h := Middleware(v, routes.User)(next)

		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer "+buyerToken)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		require.True(t, *called)
		assert.Equal(t, "u-buyer", (*seen).Header.Get(authctx.HeaderUserID))
		assert.Equal(t, "buyer", (*seen).Header.Get(authctx.HeaderUserRoles))
		id, ok := authctx.FromContext((*seen).Context())
		require.True(t, ok)
		assert.Equal(t, "u-buyer", id.UserID)
		assert.Equal(t, []string{"buyer"}, id.Roles)
	})

	t.Run("buyer on seller route is forbidden", func(t *testing.T) {
		next, called, _ := newRecordingHandler()
		h := Middleware(v, routes.Seller)(next)

		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer "+buyerToken)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusForbidden, rec.Code)
		assert.False(t, *called)
	})

	t.Run("seller on seller route is allowed with both roles", func(t *testing.T) {
		next, called, seen := newRecordingHandler()
		h := Middleware(v, routes.Seller)(next)

		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer "+sellerToken)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		require.True(t, *called)
		assert.Equal(t, "u-seller", (*seen).Header.Get(authctx.HeaderUserID))
		assert.Equal(t, "buyer,seller", (*seen).Header.Get(authctx.HeaderUserRoles))
	})

	t.Run("valid token on public route is still injected", func(t *testing.T) {
		next, called, seen := newRecordingHandler()
		h := Middleware(v, routes.Public)(next)

		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer "+buyerToken)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		require.True(t, *called)
		assert.Equal(t, "u-buyer", (*seen).Header.Get(authctx.HeaderUserID))
		assert.Equal(t, "buyer", (*seen).Header.Get(authctx.HeaderUserRoles))
	})
}

func TestMiddlewareRejectsBadCredentialsOnPublicRoute(t *testing.T) {
	k1 := authtest.NewKey(t, "k1")
	v, _ := newTestVerifier(t, k1)

	expired := k1.Sign(t, func() authtest.Claims {
		c := authtest.ValidClaims("u1", "buyer")
		c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-1 * time.Hour))
		return c
	}())

	cases := map[string]string{
		"expired token": "Bearer " + expired,
		"garbage token": "Bearer not-a-jwt",
		"Basic scheme":  "Basic dXNlcjpwYXNz",
		"empty bearer":  "Bearer ",
	}

	for name, authHeader := range cases {
		t.Run(name, func(t *testing.T) {
			next, called, _ := newRecordingHandler()
			h := Middleware(v, routes.Public)(next)

			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("Authorization", authHeader)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusUnauthorized, rec.Code)
			assert.False(t, *called)
		})
	}
}

func TestMiddlewareStripsSpoofedHeaders(t *testing.T) {
	k1 := authtest.NewKey(t, "k1")
	v, _ := newTestVerifier(t, k1)
	buyerToken := k1.Sign(t, authtest.ValidClaims("u-buyer", "buyer"))

	t.Run("spoofed headers gone on public route, anonymous", func(t *testing.T) {
		next, called, seen := newRecordingHandler()
		h := Middleware(v, routes.Public)(next)

		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("X-User-Id", "admin")
		req.Header.Set("X-User-Roles", "admin")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		require.True(t, *called)
		assert.Empty(t, (*seen).Header.Get(authctx.HeaderUserID))
		assert.Empty(t, (*seen).Header.Get(authctx.HeaderUserRoles))
	})

	t.Run("spoofed headers do not grant identity on user route", func(t *testing.T) {
		next, called, _ := newRecordingHandler()
		h := Middleware(v, routes.User)(next)

		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("X-User-Id", "admin")
		req.Header.Set("X-User-Roles", "admin")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.False(t, *called)
	})

	t.Run("buyer token with spoofed seller role header is still forbidden", func(t *testing.T) {
		next, called, _ := newRecordingHandler()
		h := Middleware(v, routes.Seller)(next)

		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer "+buyerToken)
		req.Header.Set("X-User-Roles", "seller")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusForbidden, rec.Code)
		assert.False(t, *called)
	})

	t.Run("buyer's forwarded headers have exactly one value each", func(t *testing.T) {
		next, called, seen := newRecordingHandler()
		h := Middleware(v, routes.User)(next)

		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer "+buyerToken)
		req.Header.Set("X-User-Id", "admin")
		req.Header.Set("X-User-Roles", "admin")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		require.True(t, *called)
		assert.Equal(t, []string{"u-buyer"}, (*seen).Header.Values(authctx.HeaderUserID))
		assert.Equal(t, []string{"buyer"}, (*seen).Header.Values(authctx.HeaderUserRoles))
	})
}

func TestMiddlewareCallbackIgnoresAuthorization(t *testing.T) {
	k1 := authtest.NewKey(t, "k1")
	v, _ := newTestVerifier(t, k1)
	buyerToken := k1.Sign(t, authtest.ValidClaims("u-buyer", "buyer"))

	// Expired token for testing bad credentials.
	expired := k1.Sign(t, func() authtest.Claims {
		c := authtest.ValidClaims("u1", "buyer")
		c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-1 * time.Hour))
		return c
	}())

	t.Run("no Authorization passes through with no identity", func(t *testing.T) {
		next, called, seen := newRecordingHandler()
		h := Middleware(v, routes.Callback)(next)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

		assert.Equal(t, http.StatusOK, rec.Code)
		require.True(t, *called)
		assert.Empty(t, (*seen).Header.Get(authctx.HeaderUserID))
		assert.Empty(t, (*seen).Header.Get(authctx.HeaderUserRoles))
		_, ok := authctx.FromContext((*seen).Context())
		assert.False(t, ok)
	})

	t.Run("expired token is ignored", func(t *testing.T) {
		next, called, seen := newRecordingHandler()
		h := Middleware(v, routes.Callback)(next)

		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer "+expired)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		require.True(t, *called)
		assert.Empty(t, (*seen).Header.Get(authctx.HeaderUserID))
		assert.Empty(t, (*seen).Header.Get(authctx.HeaderUserRoles))
	})

	t.Run("garbage token is ignored", func(t *testing.T) {
		next, called, seen := newRecordingHandler()
		h := Middleware(v, routes.Callback)(next)

		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer not-a-jwt")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		require.True(t, *called)
		assert.Empty(t, (*seen).Header.Get(authctx.HeaderUserID))
		assert.Empty(t, (*seen).Header.Get(authctx.HeaderUserRoles))
	})

	t.Run("malformed Authorization header is ignored", func(t *testing.T) {
		next, called, seen := newRecordingHandler()
		h := Middleware(v, routes.Callback)(next)

		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		require.True(t, *called)
		assert.Empty(t, (*seen).Header.Get(authctx.HeaderUserID))
		assert.Empty(t, (*seen).Header.Get(authctx.HeaderUserRoles))
	})

	t.Run("valid token is not verified or injected", func(t *testing.T) {
		next, called, seen := newRecordingHandler()
		h := Middleware(v, routes.Callback)(next)

		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer "+buyerToken)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		require.True(t, *called)
		assert.Empty(t, (*seen).Header.Get(authctx.HeaderUserID))
		assert.Empty(t, (*seen).Header.Get(authctx.HeaderUserRoles))
		userID := authctx.UserID((*seen).Context())
		assert.Equal(t, "", userID)
	})

	t.Run("spoofed identity headers are stripped", func(t *testing.T) {
		next, called, seen := newRecordingHandler()
		h := Middleware(v, routes.Callback)(next)

		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("X-User-Id", "admin")
		req.Header.Set("X-User-Roles", "seller")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		require.True(t, *called)
		assert.Empty(t, (*seen).Header.Get(authctx.HeaderUserID))
		assert.Empty(t, (*seen).Header.Get(authctx.HeaderUserRoles))
	})
}
