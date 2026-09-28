package main

import (
	"log/slog"
	"net/http"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/pkg/server"
)

// newHandler serves /healthz and /readyz directly and everything else through
// the standard middleware into api. A nil api answers 404 for every other route.
func newHandler(logger *slog.Logger, checks map[string]server.Check, api http.Handler) http.Handler {
	if api == nil {
		api = httpx.NotFound()
	}
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", server.Healthz())
	mux.Handle("GET /readyz", server.Readyz(checks))
	mux.Handle("/", httpx.RequestID(httpx.Recover(httpx.AccessLog(logger)(api))))
	return mux
}
