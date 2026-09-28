package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/authctx"
	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/pkg/server"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func do(h http.Handler, path string, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestHealthEndpoints(t *testing.T) {
	ok := map[string]server.Check{"postgres": func(context.Context) error { return nil }}
	down := map[string]server.Check{"postgres": func(context.Context) error { return errors.New("down") }}

	assert.Equal(t, http.StatusOK, do(newHandler(discard, ok, nil), "/healthz", nil).Code)
	assert.Equal(t, http.StatusOK, do(newHandler(discard, ok, nil), "/readyz", nil).Code)
	assert.Equal(t, http.StatusServiceUnavailable, do(newHandler(discard, down, nil), "/readyz", nil).Code)
	assert.Equal(t, http.StatusOK, do(newHandler(discard, down, nil), "/healthz", nil).Code, "liveness ignores dependencies")
}

func TestUnknownRouteIsJSON404WithRequestID(t *testing.T) {
	rec := do(newHandler(discard, nil, nil), "/api/nope", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.NotEmpty(t, rec.Header().Get(httpx.HeaderRequestID))
	var body map[string]map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "NOT_FOUND", body["error"]["code"])
}

func TestAPIReceivesIdentityFromGatewayHeaders(t *testing.T) {
	var uid string
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { uid = authctx.UserID(r.Context()) })
	do(newHandler(discard, nil, api), "/api/x", map[string]string{authctx.HeaderUserID: "u1"})
	assert.Equal(t, "u1", uid)
}
