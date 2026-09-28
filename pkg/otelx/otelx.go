// Package otelx configures OpenTelemetry tracing (OTLP gRPC exporter,
// W3C trace-context propagation) and exposes HTTP instrumentation helpers.
package otelx

import (
	"context"
	"fmt"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Setup installs the global tracer provider and propagator. endpoint is an
// OTLP gRPC URL such as "http://jaeger:4317"; when empty, spans are created
// (so trace IDs still propagate and appear in logs) but not exported.
func Setup(ctx context.Context, serviceName, endpoint string) (func(context.Context) error, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	res, err := resource.Merge(resource.Default(),
		resource.NewSchemaless(attribute.String("service.name", serviceName)))
	if err != nil {
		return nil, fmt.Errorf("otel resource: %w", err)
	}
	opts := []sdktrace.TracerProviderOption{sdktrace.WithResource(res)}
	if endpoint != "" {
		exp, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpointURL(endpoint))
		if err != nil {
			return nil, fmt.Errorf("otlp exporter: %w", err)
		}
		opts = append(opts, sdktrace.WithBatcher(exp))
	}
	tp := sdktrace.NewTracerProvider(opts...)
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}

// HTTPHandler wraps h with a server span per request.
func HTTPHandler(h http.Handler, operation string) http.Handler {
	return otelhttp.NewHandler(h, operation)
}

// HTTPTransport wraps base (http.DefaultTransport when nil) with client spans
// and traceparent injection.
func HTTPTransport(base http.RoundTripper) http.RoundTripper {
	return otelhttp.NewTransport(base)
}
