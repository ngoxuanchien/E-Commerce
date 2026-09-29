package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/services/gateway/internal/auth/authtest"
	"github.com/ngoxuanchien/e-commerce/services/gateway/internal/proxy"
	"github.com/ngoxuanchien/e-commerce/services/gateway/internal/routes"
)

// identityHeaders returns the values of every header that is an x-user-id or
// x-user-roles spelling variant, keyed by its exact name.
func identityHeaders(h http.Header) map[string][]string {
	found := map[string][]string{}
	for name, values := range h {
		n := strings.ReplaceAll(strings.ToLower(name), "_", "-")
		if n == "x-user-id" || n == "x-user-roles" {
			found[name] = values
		}
	}
	return found
}

func withBody(body []byte, chunked bool) func(*http.Request) {
	return func(r *http.Request) {
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		if chunked {
			r.ContentLength = -1
		}
	}
}

func TestEveryContractRouteEnforcesItsAuthLevel(t *testing.T) {
	cases := []struct {
		method, target, upstream string
		level                    routes.Level
	}{
		// identity.yaml
		{"POST", "/api/auth/register", routes.Identity, routes.Public},
		{"POST", "/api/auth/login", routes.Identity, routes.Public},
		{"POST", "/api/auth/refresh", routes.Identity, routes.Public},
		{"POST", "/api/auth/logout", routes.Identity, routes.Public},
		{"GET", "/api/users/me", routes.Identity, routes.User},
		// catalog.yaml
		{"GET", "/api/categories", routes.Catalog, routes.Public},
		{"GET", "/api/products?q=shirt&sort=price_asc&limit=20", routes.Catalog, routes.Public},
		{"GET", "/api/products/p1", routes.Catalog, routes.Public},
		{"GET", "/api/shops/nha-sach", routes.Catalog, routes.Public},
		{"POST", "/api/shops", routes.Catalog, routes.User},
		{"GET", "/api/shops/me", routes.Catalog, routes.Seller},
		{"PATCH", "/api/shops/me", routes.Catalog, routes.Seller},
		{"GET", "/api/seller/products?status=active&limit=10", routes.Catalog, routes.Seller},
		{"POST", "/api/seller/products", routes.Catalog, routes.Seller},
		{"PATCH", "/api/seller/products/p1", routes.Catalog, routes.Seller},
		{"DELETE", "/api/seller/products/p1", routes.Catalog, routes.Seller},
		{"POST", "/api/seller/uploads", routes.Catalog, routes.Seller},
		// order.yaml
		{"GET", "/api/cart", routes.Order, routes.User},
		{"PUT", "/api/cart/items/s1", routes.Order, routes.User},
		{"DELETE", "/api/cart/items/s1", routes.Order, routes.User},
		{"POST", "/api/checkout", routes.Order, routes.User},
		{"GET", "/api/orders?status=PENDING&limit=5", routes.Order, routes.User},
		{"GET", "/api/orders/o1", routes.Order, routes.User},
		{"POST", "/api/orders/o1/cancel", routes.Order, routes.User},
		{"POST", "/api/orders/o1/complete", routes.Order, routes.User},
		{"GET", "/api/seller/orders?status=CONFIRMED", routes.Order, routes.Seller},
		{"POST", "/api/seller/orders/o1/confirm", routes.Order, routes.Seller},
		{"POST", "/api/seller/orders/o1/ship", routes.Order, routes.Seller},
		{"POST", "/api/seller/orders/o1/deliver", routes.Order, routes.Seller},
		{"POST", "/api/seller/orders/o1/cancel", routes.Order, routes.Seller},
	}
	require.Len(t, cases, 30, "must cover all 30 WS0 §4 operations")

	h := newHarness(t)
	buyer := h.token("u-buyer", "buyer")
	seller := h.token("u-seller", "buyer", "seller")

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.target, func(t *testing.T) {
			assertProxied := func(rec *httptest.ResponseRecorder, wantID, wantRoles string) {
				t.Helper()
				e := h.proxied(rec)
				assert.Equal(t, tc.upstream, e.Upstream)
				assert.Equal(t, tc.target, e.URI)
				if wantID == "" {
					assert.Empty(t, identityHeaders(e.Headers))
					return
				}
				assert.Equal(t, map[string][]string{
					"X-User-Id":    {wantID},
					"X-User-Roles": {wantRoles},
				}, identityHeaders(e.Headers))
			}

			anon := h.do(tc.method, tc.target)
			asBuyer := h.do(tc.method, tc.target, withToken(buyer))
			asSeller := h.do(tc.method, tc.target, withToken(seller))

			switch tc.level {
			case routes.Public:
				assertProxied(anon, "", "")
				assertProxied(asBuyer, "u-buyer", "buyer")
			case routes.User:
				requireError(t, anon, http.StatusUnauthorized, httpx.CodeUnauthenticated)
				assertProxied(asBuyer, "u-buyer", "buyer")
			case routes.Seller:
				requireError(t, anon, http.StatusUnauthorized, httpx.CodeUnauthenticated)
				requireError(t, asBuyer, http.StatusForbidden, httpx.CodeForbidden)
			}
			assertProxied(asSeller, "u-seller", "buyer,seller")
		})
	}
}

func TestUnknownRouteIsJSON404WithRequestID(t *testing.T) {
	h := newHarness(t)

	rec := h.do("GET", "/api/nope")

	requireError(t, rec, http.StatusNotFound, httpx.CodeNotFound)
	assert.NotEmpty(t, rec.Header().Get(httpx.HeaderRequestID))
	assert.Zero(t, h.totalHits())
}

func TestClientCannotInjectIdentityHeaders(t *testing.T) {
	h := newHarness(t)
	buyer := h.token("u-buyer", "buyer")

	t.Run("anonymous public request reaches upstream without identity in any spelling", func(t *testing.T) {
		req := func(r *http.Request) {
			r.Header.Set("X-User-Id", "evil")
			r.Header.Set("X-User-Roles", "seller")
			r.Header["x-user-id"] = []string{"evil"}
			r.Header["X_User_Id"] = []string{"evil"}
			r.Header["x_user_roles"] = []string{"seller"}
		}
		e := h.proxied(h.do("GET", "/api/categories", req))
		assert.Empty(t, identityHeaders(e.Headers))
	})

	t.Run("anonymous cannot use spoofed identity on a user route", func(t *testing.T) {
		rec := h.do("GET", "/api/cart", withHeader("X-User-Id", "evil"), withHeader("X-User-Roles", "buyer"))
		requireError(t, rec, http.StatusUnauthorized, httpx.CodeUnauthenticated)
	})

	t.Run("buyer cannot spoof the seller role", func(t *testing.T) {
		rec := h.do("GET", "/api/seller/products", withToken(buyer), withHeader("X-User-Roles", "seller"))
		requireError(t, rec, http.StatusForbidden, httpx.CodeForbidden)
	})

	t.Run("token identity replaces spoofed values", func(t *testing.T) {
		rec := h.do("GET", "/api/cart", withToken(buyer),
			withHeader("X-User-Id", "evil"), withHeader("X-User-Roles", "seller"),
			func(r *http.Request) {
				r.Header["x-user-id"] = []string{"evil2"}
				r.Header["X_User_Roles"] = []string{"seller"}
			})
		e := h.proxied(rec)
		assert.Equal(t, map[string][]string{
			"X-User-Id":    {"u-buyer"},
			"X-User-Roles": {"buyer"},
		}, identityHeaders(e.Headers))
	})
}

func TestInvalidTokensAreRejectedEvenOnPublicRoutes(t *testing.T) {
	h := newHarness(t)

	expired := authtest.ValidClaims("u1")
	expired.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Hour))
	wrongIssuer := authtest.ValidClaims("u1")
	wrongIssuer.Issuer = "someone-else"

	tokens := map[string]string{
		"expired":      h.key.Sign(t, expired),
		"wrong issuer": h.key.Sign(t, wrongIssuer),
		"wrong key":    authtest.NewKey(t, testKID).Sign(t, authtest.ValidClaims("u1")),
		"garbage":      "not.a.jwt",
	}
	for name, tok := range tokens {
		for _, target := range []string{"/api/products", "/api/cart"} {
			t.Run(name+" "+target, func(t *testing.T) {
				rec := h.do("GET", target, withToken(tok))
				requireError(t, rec, http.StatusUnauthorized, httpx.CodeUnauthenticated)
			})
		}
	}
	assert.Zero(t, h.totalHits())
}

func TestNonCanonicalPathsCannotDowngradeAuth(t *testing.T) {
	h := newHarness(t)
	buyer := h.token("u-buyer", "buyer")

	t.Run("encoded and slash variants still hit the seller rule", func(t *testing.T) {
		for _, target := range []string{"/api/shops/me/", "/api/shops/m%65", "/api/seller/%70roducts"} {
			rec := h.do("GET", target, withToken(buyer))
			requireError(t, rec, http.StatusForbidden, httpx.CodeForbidden)
		}
	})

	t.Run("ServeMux redirects unclean paths instead of proxying them", func(t *testing.T) {
		// Go 1.27's ServeMux answers 307 (not 301) for GET; either way the
		// request is redirected to the cleaned path and never proxied.
		for target, cleaned := range map[string]string{
			"/api/products/../shops/me": "/api/shops/me",
			"/api//shops/me":            "/api/shops/me",
			"/api/./shops/me":           "/api/shops/me",
		} {
			rec := h.do("GET", target, withToken(buyer))
			assert.Equal(t, http.StatusTemporaryRedirect, rec.Code, target)
			assert.Equal(t, cleaned, rec.Header().Get("Location"), target)
		}
	})
	assert.Zero(t, h.hitCount(routes.Catalog))
}

func TestHealthEndpoints(t *testing.T) {
	h := newHarness(t, skipJWKSLoad())

	assert.Equal(t, http.StatusOK, h.do("GET", "/healthz").Code)
	assert.Equal(t, http.StatusServiceUnavailable, h.do("GET", "/readyz").Code)

	require.NoError(t, h.jwks.Refresh(t.Context()))
	assert.Equal(t, http.StatusOK, h.do("GET", "/readyz").Code)
}

func TestUpstreamDownIs503WithRequestID(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	target, err := proxy.ParseTarget(dead.URL)
	require.NoError(t, err)
	dead.Close()
	h := newHarness(t, withUpstream(routes.Catalog, proxy.New(target, proxy.NewTransport(time.Second))))

	rec := h.do("GET", "/api/products", withHeader(httpx.HeaderRequestID, "req-123"))

	requireError(t, rec, http.StatusServiceUnavailable, httpx.CodeUpstreamUnavailable)
	assert.Equal(t, "req-123", rec.Header().Get(httpx.HeaderRequestID))
}

func TestRequestIDIsForwardedAndEchoed(t *testing.T) {
	h := newHarness(t)

	t.Run("valid id is reused", func(t *testing.T) {
		rec := h.do("GET", "/api/categories", withHeader(httpx.HeaderRequestID, "req-abc.1"))
		e := h.proxied(rec)
		assert.Equal(t, "req-abc.1", rec.Header().Get(httpx.HeaderRequestID))
		assert.Equal(t, "req-abc.1", e.Headers.Get(httpx.HeaderRequestID))
	})

	t.Run("invalid id is replaced consistently", func(t *testing.T) {
		rec := h.do("GET", "/api/categories", withHeader(httpx.HeaderRequestID, "bad id!"))
		e := h.proxied(rec)
		echoedID := rec.Header().Get(httpx.HeaderRequestID)
		assert.NotEmpty(t, echoedID)
		assert.NotEqual(t, "bad id!", echoedID)
		assert.Equal(t, echoedID, e.Headers.Get(httpx.HeaderRequestID))
	})
}

func TestTraceparentIsForwarded(t *testing.T) {
	h := newHarness(t)
	const traceID, spanID = "0af7651916cd43dd8448eb211c80319c", "b7ad6b7169203331"

	e := h.proxied(h.do("GET", "/api/categories", withHeader("Traceparent", "00-"+traceID+"-"+spanID+"-01")))

	parts := strings.Split(e.Headers.Get("Traceparent"), "-")
	require.Len(t, parts, 4)
	assert.Equal(t, traceID, parts[1])
	assert.NotEqual(t, spanID, parts[2], "the gateway starts its own span")
}

func TestUpstreamResponseIsPassedThroughUntouched(t *testing.T) {
	const body = `{"error":{"code":"OUT_OF_STOCK","message":"not enough stock","details":{"shortages":[{"sku_id":"s1"}]}}}`
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(stub.Close)
	target, err := proxy.ParseTarget(stub.URL)
	require.NoError(t, err)
	h := newHarness(t, withUpstream(routes.Order, proxy.New(target, proxy.NewTransport(time.Second))))

	rec := h.do("POST", "/api/checkout", withToken(h.token("u1", "buyer")))

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, body, rec.Body.String())
}

func TestPanicBecomes500(t *testing.T) {
	h := newHarness(t, withUpstream(routes.Catalog, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})))

	requireError(t, h.do("GET", "/api/products"), http.StatusInternalServerError, httpx.CodeInternal)
}

func TestCORS(t *testing.T) {
	h := newHarness(t)
	preflight := func(origin string) *httptest.ResponseRecorder {
		return h.do("OPTIONS", "/api/cart",
			withHeader("Origin", origin),
			withHeader("Access-Control-Request-Method", "PUT"))
	}

	allowed := preflight(allowedOrigin)
	assert.Equal(t, http.StatusNoContent, allowed.Code)
	assert.Equal(t, allowedOrigin, allowed.Header().Get("Access-Control-Allow-Origin"))

	requireError(t, preflight("https://evil.example"), http.StatusForbidden, httpx.CodeForbidden)
	assert.Zero(t, h.totalHits())

	rec := h.do("GET", "/api/categories", withHeader("Origin", allowedOrigin))
	h.proxied(rec)
	assert.Equal(t, allowedOrigin, rec.Header().Get("Access-Control-Allow-Origin"))
}

func TestRateLimits(t *testing.T) {
	t.Run("login is limited to 10 per minute", func(t *testing.T) {
		h := newHarness(t, withProductionLimits())
		for i := range 10 {
			require.Equal(t, http.StatusOK, h.do("POST", "/api/auth/login").Code, "request %d", i+1)
		}

		rec := h.do("POST", "/api/auth/login")

		requireError(t, rec, http.StatusTooManyRequests, httpx.CodeRateLimited)
		retry, err := strconv.Atoi(rec.Header().Get("Retry-After"))
		require.NoError(t, err)
		assert.Positive(t, retry)
		assert.EqualValues(t, 10, h.hitCount(routes.Identity))
	})

	t.Run("everything else allows a burst of 40", func(t *testing.T) {
		h := newHarness(t, withProductionLimits())
		for i := range 40 {
			require.Equal(t, http.StatusOK, h.do("GET", "/api/categories").Code, "request %d", i+1)
		}

		rec := h.do("GET", "/api/categories")

		requireError(t, rec, http.StatusTooManyRequests, httpx.CodeRateLimited)
		assert.Equal(t, "1", rec.Header().Get("Retry-After"))
	})
}

func TestBodyLimit(t *testing.T) {
	h := newHarness(t)
	seller := withToken(h.token("u-seller", "seller"))
	post := func(size int, chunked bool) *httptest.ResponseRecorder {
		return h.do("POST", "/api/seller/products", seller, withBody(make([]byte, size), chunked))
	}

	assert.Equal(t, http.StatusOK, post(httpx.MaxBodyBytes, false).Code)
	requireError(t, post(httpx.MaxBodyBytes+1, false), http.StatusRequestEntityTooLarge, httpx.CodePayloadTooLarge)
	// Only the exactly-1-MB request may have reached the catalog so far.
	assert.EqualValues(t, 1, h.hitCount(routes.Catalog))

	requireError(t, post(httpx.MaxBodyBytes+1, true), http.StatusRequestEntityTooLarge, httpx.CodePayloadTooLarge)
}

func TestAccessLogHasRouteAndUserButNoSecrets(t *testing.T) {
	h := newHarness(t)
	tok := h.token("u-seller", "seller")

	h.proxied(h.do("GET", "/api/seller/products?secret=hunter2", withToken(tok)))
	h.proxied(h.do("GET", "/api/products/p1"))
	requireError(t, h.do("GET", "/api/nope"), http.StatusNotFound, httpx.CodeNotFound)

	logs := h.logs.String()
	for _, want := range []string{
		`"route":"/api/seller/products"`,
		`"user_id":"u-seller"`,
		`"status":200`,
		`"route":"unmatched"`,
		`"request_id":`,
	} {
		assert.Contains(t, logs, want)
	}
	for _, secret := range []string{tok, "hunter2", "/p1"} {
		assert.NotContains(t, logs, secret)
	}
	for line := range strings.SplitSeq(strings.TrimSpace(logs), "\n") {
		assert.True(t, json.Valid([]byte(line)), line)
	}
}

func TestNewHandlerRejectsMissingUpstream(t *testing.T) {
	g := &gateway{
		routes:    routes.Default(),
		upstreams: map[string]http.Handler{routes.Identity: http.NotFoundHandler(), routes.Catalog: http.NotFoundHandler()},
	}

	_, err := newHandler(g)

	require.Error(t, err)
	assert.Contains(t, err.Error(), routes.Order)
}
