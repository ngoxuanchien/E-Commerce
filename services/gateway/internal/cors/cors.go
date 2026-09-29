// Package cors answers CORS preflights and annotates actual responses for a
// configured allow-list of origins, so the gateway can serve a browser SPA
// with credentialed requests (the refresh cookie) without a wildcard origin.
package cors

import (
	"net/http"
	"strings"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
)

const (
	allowMethods  = "GET, POST, PUT, PATCH, DELETE"
	allowHeaders  = "Authorization, Content-Type, Idempotency-Key, X-Request-Id"
	exposeHeaders = "X-Request-Id, Retry-After"
	maxAge        = "600"
)

// Middleware returns middleware that answers CORS preflights and sets CORS
// response headers for the given allowed origins. Origins are matched
// exactly; entries are trimmed and blank entries are ignored. A wildcard
// origin is never emitted, since credentialed requests (the refresh cookie)
// require an explicit origin.
//
// A request without an Origin header passes through untouched. A preflight
// (OPTIONS with Access-Control-Request-Method) from an allowed origin is
// answered here with 204 and no call to next; from a disallowed origin it is
// rejected with 403 FORBIDDEN and no CORS headers. An actual request from an
// allowed origin reaches next with CORS response headers added; from a
// disallowed origin it reaches next unchanged.
func Middleware(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := normalizeOrigins(allowedOrigins)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			preflight := isPreflight(r)
			addVary(w, preflight)

			if !allowed[origin] {
				if preflight {
					httpx.WriteError(w, r, httpx.NewError(httpx.CodeForbidden, "origin not allowed"))
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")

			if preflight {
				w.Header().Set("Access-Control-Allow-Methods", allowMethods)
				w.Header().Set("Access-Control-Allow-Headers", allowHeaders)
				w.Header().Set("Access-Control-Max-Age", maxAge)
				w.WriteHeader(http.StatusNoContent)
				return
			}

			w.Header().Set("Access-Control-Expose-Headers", exposeHeaders)
			next.ServeHTTP(w, r)
		})
	}
}

// isPreflight reports whether r is a CORS preflight request.
func isPreflight(r *http.Request) bool {
	return r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
}

// addVary marks the response as varying by Origin, and additionally by the
// preflight request headers when preflight is true, so caches do not conflate
// responses for different origins or preflight requests.
func addVary(w http.ResponseWriter, preflight bool) {
	w.Header().Add("Vary", "Origin")
	if preflight {
		w.Header().Add("Vary", "Access-Control-Request-Method")
		w.Header().Add("Vary", "Access-Control-Request-Headers")
	}
}

// normalizeOrigins trims each origin and returns the non-blank ones as a set
// for exact-match lookup.
func normalizeOrigins(origins []string) map[string]bool {
	set := make(map[string]bool, len(origins))
	for _, o := range origins {
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		set[o] = true
	}
	return set
}
