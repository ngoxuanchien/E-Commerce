package cors_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/gateway/internal/cors"
)

var allowedOrigins = []string{"http://localhost:3000", "http://localhost:5173"}

func nextSpy(called *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*called = true
		w.WriteHeader(http.StatusOK)
	})
}

func TestMiddleware_AllowedPreflightRespondsWithoutCallingNext(t *testing.T) {
	called := false
	handler := cors.Middleware(allowedOrigins)(nextSpy(&called))

	req := httptest.NewRequest(http.MethodOptions, "/api/products", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "Content-Type")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.False(t, called, "next must not be called for a handled preflight")
	require.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "http://localhost:3000", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", rec.Header().Get("Access-Control-Allow-Credentials"))
	assert.Equal(t, "GET, POST, PUT, PATCH, DELETE", rec.Header().Get("Access-Control-Allow-Methods"))
	assert.Equal(t, "Authorization, Content-Type, Idempotency-Key, X-Request-Id", rec.Header().Get("Access-Control-Allow-Headers"))
	assert.Equal(t, "600", rec.Header().Get("Access-Control-Max-Age"))
	assert.ElementsMatch(t, []string{"Origin", "Access-Control-Request-Method", "Access-Control-Request-Headers"}, rec.Result().Header.Values("Vary"))
}

func TestMiddleware_DisallowedPreflightIsForbiddenWithNoCORSHeaders(t *testing.T) {
	tests := []struct {
		name   string
		origin string
	}{
		{"different scheme and host", "https://evil.example"},
		{"subdomain suffix attack", "http://localhost:3000.evil.example"},
		{"case mismatch", "http://LOCALHOST:3000"},
		{"opaque origin", "null"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			handler := cors.Middleware(allowedOrigins)(nextSpy(&called))

			req := httptest.NewRequest(http.MethodOptions, "/api/products", nil)
			req.Header.Set("Origin", tt.origin)
			req.Header.Set("Access-Control-Request-Method", "POST")
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assert.False(t, called, "next must not be called for a rejected preflight")
			require.Equal(t, http.StatusForbidden, rec.Code)
			assert.Contains(t, rec.Body.String(), `"code":"FORBIDDEN"`)
			assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
			assert.Empty(t, rec.Header().Get("Access-Control-Allow-Credentials"))
			assert.Empty(t, rec.Header().Get("Access-Control-Allow-Methods"))
			assert.Empty(t, rec.Header().Get("Access-Control-Allow-Headers"))
			assert.Empty(t, rec.Header().Get("Access-Control-Max-Age"))
		})
	}
}

func TestMiddleware_ActualRequestFromAllowedOrigin(t *testing.T) {
	called := false
	handler := cors.Middleware(allowedOrigins)(nextSpy(&called))

	req := httptest.NewRequest(http.MethodGet, "/api/products", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.True(t, called, "next must be called for an actual request")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "http://localhost:5173", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", rec.Header().Get("Access-Control-Allow-Credentials"))
	assert.Equal(t, "X-Request-Id, Retry-After", rec.Header().Get("Access-Control-Expose-Headers"))
	assert.Equal(t, []string{"Origin"}, rec.Result().Header.Values("Vary"))
}

func TestMiddleware_ActualRequestFromDisallowedOriginPassesThroughWithoutCORSHeaders(t *testing.T) {
	called := false
	handler := cors.Middleware(allowedOrigins)(nextSpy(&called))

	req := httptest.NewRequest(http.MethodGet, "/api/products", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.True(t, called, "next must still be called for a disallowed actual request")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Credentials"))
	assert.Empty(t, rec.Header().Get("Access-Control-Expose-Headers"))
}

func TestMiddleware_OptionsWithoutOriginPassesThroughUntouched(t *testing.T) {
	called := false
	handler := cors.Middleware(allowedOrigins)(nextSpy(&called))

	req := httptest.NewRequest(http.MethodOptions, "/api/products", nil)
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.True(t, called, "next must be called when there is no Origin header")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Empty(t, rec.Header().Get("Vary"))
}

func TestMiddleware_TrimsAndIgnoresBlankOrigins(t *testing.T) {
	called := false
	handler := cors.Middleware([]string{" http://localhost:3000 ", "", "  "})(nextSpy(&called))

	req := httptest.NewRequest(http.MethodGet, "/api/products", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.True(t, called)
	assert.Equal(t, "http://localhost:3000", rec.Header().Get("Access-Control-Allow-Origin"))
}
