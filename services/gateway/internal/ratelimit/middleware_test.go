package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestMiddleware_LoginAndRegisterShareBucket(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	login := NewLoginLimiter(WithClock(clock.Now))
	general := NewGeneralLimiter(WithClock(clock.Now))
	clientIP := func(r *http.Request) string { return "203.0.113.9" }
	handler := Middleware(general, login, clientIP)(okHandler())

	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
		handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
	}
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/auth/register", nil)
		handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
	}

	// The bucket is now exhausted (10 total between login+register); the
	// 11th request, with a tolerated trailing slash, is denied.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login/", nil)
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Equal(t, "6", rec.Header().Get("Retry-After"))
	assert.Contains(t, rec.Body.String(), `"code":"RATE_LIMITED"`)

	// A different route draws from the general bucket and still passes.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/products", nil)
	handler.ServeHTTP(rec2, req2)
	assert.Equal(t, http.StatusOK, rec2.Code)
}

func TestMiddleware_GeneralLimiterRetryAfterRoundsUpToOneSecond(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	login := NewLoginLimiter(WithClock(clock.Now))
	general := NewGeneralLimiter(WithClock(clock.Now))
	clientIP := func(r *http.Request) string { return "203.0.113.10" }
	handler := Middleware(general, login, clientIP)(okHandler())

	for i := 0; i < 40; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/products", nil)
		handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/products", nil)
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	// 50ms real wait rounds up to a whole-second minimum of 1.
	assert.Equal(t, "1", rec.Header().Get("Retry-After"))
}
