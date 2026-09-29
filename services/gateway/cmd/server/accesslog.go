package main

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// logInfo is filled in by handlers deeper in the pipeline so the access log,
// which wraps them, can report what they learned.
type logInfo struct {
	route  string
	userID string
}

type logInfoKey struct{}

// logInfoFrom returns the request's logInfo, or a throwaway one when the
// request did not pass through accessLog.
func logInfoFrom(ctx context.Context) *logInfo {
	if info, ok := ctx.Value(logInfoKey{}).(*logInfo); ok {
		return info
	}
	return &logInfo{}
}

// statusRecorder captures the response status for the access log.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer, which
// keeps flushing and streaming responses working.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// accessLog logs one line per request: method, matched route pattern (never
// the raw path or query), status, duration and, when authenticated, the user
// ID. It never logs headers or bodies.
func accessLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			info := &logInfo{}
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), logInfoKey{}, info)))

			if rec.status == 0 {
				rec.status = http.StatusOK
			}
			route := info.route
			if route == "" {
				route = "unmatched"
			}
			attrs := []slog.Attr{
				slog.String("method", r.Method),
				slog.String("route", route),
				slog.Int("status", rec.status),
				slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
			}
			if info.userID != "" {
				attrs = append(attrs, slog.String("user_id", info.userID))
			}
			logger.LogAttrs(r.Context(), slog.LevelInfo, "http request", attrs...)
		})
	}
}
