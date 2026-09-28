package otelx_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/ngoxuanchien/e-commerce/pkg/otelx"
)

func TestSetupWithoutEndpointStillTracesAndPropagates(t *testing.T) {
	shutdown, err := otelx.Setup(context.Background(), "test-svc", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	ctx, span := otel.Tracer("t").Start(context.Background(), "op")
	defer span.End()
	require.True(t, span.SpanContext().IsValid())

	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	assert.Contains(t, carrier["traceparent"], span.SpanContext().TraceID().String())
}

func TestSetupWithEndpoint(t *testing.T) {
	shutdown, err := otelx.Setup(context.Background(), "test-svc", "http://127.0.0.1:4317")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = shutdown(ctx) // nothing listens; only must not hang past the deadline
}

func TestHTTPHandlerContinuesIncomingTrace(t *testing.T) {
	shutdown, err := otelx.Setup(context.Background(), "test-svc", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	var got trace.SpanContext
	h := otelx.HTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = trace.SpanContextFromContext(r.Context())
	}), "test")

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("traceparent", "00-0102030405060708090a0b0c0d0e0f10-0102030405060708-01")
	h.ServeHTTP(httptest.NewRecorder(), r)

	assert.Equal(t, "0102030405060708090a0b0c0d0e0f10", got.TraceID().String())
}

func TestHTTPTransportInjectsTraceparent(t *testing.T) {
	shutdown, err := otelx.Setup(context.Background(), "test-svc", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	var header string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header = r.Header.Get("traceparent")
	}))
	defer srv.Close()

	ctx, span := otel.Tracer("t").Start(context.Background(), "client")
	defer span.End()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := (&http.Client{Transport: otelx.HTTPTransport(nil)}).Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()

	assert.Contains(t, header, span.SpanContext().TraceID().String())
}
