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

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/pkg/server"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func do(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestHealthEndpoints(t *testing.T) {
	down := map[string]server.Check{"jwks": func(context.Context) error { return errors.New("not loaded") }}

	assert.Equal(t, http.StatusOK, do(newHandler(discard, nil, nil), "/healthz").Code)
	assert.Equal(t, http.StatusOK, do(newHandler(discard, nil, nil), "/readyz").Code)
	assert.Equal(t, http.StatusServiceUnavailable, do(newHandler(discard, down, nil), "/readyz").Code)
}

func TestUnknownRouteIsJSON404WithRequestID(t *testing.T) {
	rec := do(newHandler(discard, nil, nil), "/api/nope")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.NotEmpty(t, rec.Header().Get(httpx.HeaderRequestID))
	var body map[string]map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "NOT_FOUND", body["error"]["code"])
}
