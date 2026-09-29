package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ngoxuanchien/e-commerce/pkg/server"
)

func TestHealthzAndReadyz(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	up := func(context.Context) error { return nil }
	h := newHandler(logger, map[string]server.Check{"postgres": up, "kafka": up}, nil)
	for path, want := range map[string]int{"/healthz": 200, "/readyz": 200, "/api/anything": 404} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equal(t, want, rec.Code, path)
	}

	down := func(context.Context) error { return errors.New("down") }
	rec := httptest.NewRecorder()
	newHandler(logger, map[string]server.Check{"postgres": up, "kafka": down}, nil).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
