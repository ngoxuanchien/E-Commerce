package ratelimit

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
)

// Middleware rate-limits requests by client IP before they reach next.
// Requests to /api/auth/login and /api/auth/register (one trailing slash
// tolerated) draw from login; every other request draws from general. A
// denied request gets a 429 RATE_LIMITED response with a Retry-After header
// in whole seconds, rounded up, minimum 1.
func Middleware(general, login *Limiter, clientIP func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			lim := general
			if isLoginRoute(r.URL.Path) {
				lim = login
			}

			allowed, wait := lim.Allow(clientIP(r))
			if !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(retrySeconds(wait)))
				httpx.WriteError(w, r, httpx.NewError(httpx.CodeRateLimited, "rate limit exceeded"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// isLoginRoute reports whether path is /api/auth/login or
// /api/auth/register, tolerating a single trailing slash.
func isLoginRoute(path string) bool {
	path = strings.TrimSuffix(path, "/")
	return path == "/api/auth/login" || path == "/api/auth/register"
}

// retrySeconds rounds d up to a whole number of seconds, with a minimum of 1.
func retrySeconds(d time.Duration) int {
	s := int(math.Ceil(d.Seconds()))
	if s < 1 {
		return 1
	}
	return s
}
