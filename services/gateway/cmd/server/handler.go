package main

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/ngoxuanchien/e-commerce/pkg/authctx"
	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/pkg/otelx"
	"github.com/ngoxuanchien/e-commerce/pkg/server"
	"github.com/ngoxuanchien/e-commerce/services/gateway/internal/auth"
	"github.com/ngoxuanchien/e-commerce/services/gateway/internal/cors"
	"github.com/ngoxuanchien/e-commerce/services/gateway/internal/ratelimit"
	"github.com/ngoxuanchien/e-commerce/services/gateway/internal/routes"
)

// serviceName names the server span of every proxied request.
const serviceName = "gateway"

// gateway holds everything the request pipeline is assembled from.
type gateway struct {
	logger    *slog.Logger
	checks    map[string]server.Check
	routes    routes.Table
	upstreams map[string]http.Handler
	verifier  *auth.Verifier
	origins   []string
	general   *ratelimit.Limiter
	login     *ratelimit.Limiter
	clientIP  func(*http.Request) string
}

// newHandler serves /healthz and /readyz directly and everything else through
// RequestID, tracing, access log, panic recovery, CORS, rate limiting and the
// body limit into the route dispatcher.
func newHandler(g *gateway) (http.Handler, error) {
	dispatch, err := g.dispatcher()
	if err != nil {
		return nil, err
	}

	var api http.Handler = dispatch
	api = bodyLimit(httpx.MaxBodyBytes)(api)
	api = ratelimit.Middleware(g.general, g.login, g.clientIP)(api)
	api = cors.Middleware(g.origins)(api)
	api = httpx.Recover(api)
	api = accessLog(g.logger)(api)
	api = otelx.HTTPHandler(api, serviceName)
	api = httpx.RequestID(api)

	mux := http.NewServeMux()
	mux.Handle("GET /healthz", server.Healthz())
	mux.Handle("GET /readyz", server.Readyz(g.checks))
	mux.Handle("/", api)
	return mux, nil
}

// dispatcher matches each request against the route table and hands it to
// that row's auth gate and upstream. The per-row chains are built once, so an
// upstream missing from g.upstreams fails at startup rather than per request.
func (g *gateway) dispatcher() (http.Handler, error) {
	chains := make([]http.Handler, len(g.routes))
	for i, route := range g.routes {
		upstream, ok := g.upstreams[route.Upstream]
		if !ok {
			return nil, fmt.Errorf("route %s: no upstream %q configured", route.Pattern, route.Upstream)
		}
		chains[i] = auth.Middleware(g.verifier, route.Auth)(recordUser(upstream))
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i, ok := g.routes.Match(r.Method, r.URL.Path)
		if !ok {
			httpx.WriteError(w, r, httpx.NewError(httpx.CodeNotFound, "route not found"))
			return
		}
		logInfoFrom(r.Context()).route = g.routes[i].Pattern
		chains[i].ServeHTTP(w, r)
	}), nil
}

// recordUser hands the verified user ID to the access log.
func recordUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logInfoFrom(r.Context()).userID = authctx.UserID(r.Context())
		next.ServeHTTP(w, r)
	})
}

// bodyLimit rejects request bodies larger than limit. A declared
// Content-Length over the limit is refused immediately; otherwise the body is
// capped while it streams, and the proxy maps the overflow to 413.
func bodyLimit(limit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > limit {
				httpx.WriteError(w, r, httpx.NewError(httpx.CodePayloadTooLarge, "request body too large"))
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			next.ServeHTTP(w, r)
		})
	}
}
