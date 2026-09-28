package logx_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"

	"github.com/ngoxuanchien/e-commerce/pkg/logx"
)

func decode(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	return got
}

func TestLoggerAddsRequestAndTraceIDs(t *testing.T) {
	var buf bytes.Buffer
	log := logx.New(&buf, "debug")

	tid, _ := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	sid, _ := trace.SpanIDFromHex("0102030405060708")
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled,
	}))
	ctx = logx.WithRequestID(ctx, "req-1")

	log.InfoContext(ctx, "hello", "k", "v")

	got := decode(t, &buf)
	assert.Equal(t, "hello", got["msg"])
	assert.Equal(t, "v", got["k"])
	assert.Equal(t, "req-1", got["request_id"])
	assert.Equal(t, "0102030405060708090a0b0c0d0e0f10", got["trace_id"])
	assert.Equal(t, "0102030405060708", got["span_id"])
}

func TestLoggerOmitsMissingContextValues(t *testing.T) {
	var buf bytes.Buffer
	logx.New(&buf, "info").With("svc", "x").InfoContext(context.Background(), "hi")

	got := decode(t, &buf)
	assert.NotContains(t, got, "request_id")
	assert.NotContains(t, got, "trace_id")
	assert.Equal(t, "x", got["svc"])
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	logx.New(&buf, "warn").Info("dropped")
	assert.Empty(t, buf.String())
}

func TestParseLevel(t *testing.T) {
	assert.Equal(t, slog.LevelDebug, logx.ParseLevel("debug"))
	assert.Equal(t, slog.LevelWarn, logx.ParseLevel("WARN"))
	assert.Equal(t, slog.LevelInfo, logx.ParseLevel("bogus"))
	assert.Equal(t, slog.LevelInfo, logx.ParseLevel(""))
}

func TestRequestIDRoundTrip(t *testing.T) {
	assert.Equal(t, "", logx.RequestID(context.Background()))
	assert.Equal(t, "abc", logx.RequestID(logx.WithRequestID(context.Background(), "abc")))
}
