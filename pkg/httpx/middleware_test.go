package httpx_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/pkg/logx"
)

func captureRequestID(seen *string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = logx.RequestID(r.Context())
	})
}

func TestRequestIDGeneratesWhenMissing(t *testing.T) {
	var seen string
	rec := httptest.NewRecorder()
	httpx.RequestID(captureRequestID(&seen)).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	id := rec.Header().Get(httpx.HeaderRequestID)
	parsed, err := uuid.Parse(id)
	require.NoError(t, err)
	assert.Equal(t, uuid.Version(7), parsed.Version())
	assert.Equal(t, id, seen)
}

func TestRequestIDReusesValidIncoming(t *testing.T) {
	var seen string
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set(httpx.HeaderRequestID, "abc-123_x.y")
	rec := httptest.NewRecorder()
	httpx.RequestID(captureRequestID(&seen)).ServeHTTP(rec, r)

	assert.Equal(t, "abc-123_x.y", rec.Header().Get(httpx.HeaderRequestID))
	assert.Equal(t, "abc-123_x.y", seen)
}

func TestRequestIDReplacesInvalidIncoming(t *testing.T) {
	for _, bad := range []string{"has space", "line\nbreak", strings.Repeat("a", 129), `"quoted"`} {
		var seen string
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set(httpx.HeaderRequestID, bad)
		rec := httptest.NewRecorder()
		httpx.RequestID(captureRequestID(&seen)).ServeHTTP(rec, r)

		assert.NotEqual(t, bad, seen)
		_, err := uuid.Parse(seen)
		assert.NoError(t, err, "input %q", bad)
	}
}

func TestRequestIDIsForwardedOnRequestHeader(t *testing.T) {
	var header string
	h := httpx.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header = r.Header.Get(httpx.HeaderRequestID)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	assert.Equal(t, rec.Header().Get(httpx.HeaderRequestID), header)
}

func TestRecoverTurnsPanicIntoInternalError(t *testing.T) {
	rec := httptest.NewRecorder()
	httpx.Recover(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.JSONEq(t, `{"error":{"code":"INTERNAL","message":"internal server error"}}`, rec.Body.String())
}

func TestAccessLogRecordsRequest(t *testing.T) {
	var buf bytes.Buffer
	logger := logx.New(&buf, "info")
	h := httpx.RequestID(httpx.AccessLog(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("hello"))
	})))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/things?secret=1", nil))

	var entry map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
	assert.Equal(t, "http request", entry["msg"])
	assert.Equal(t, "POST", entry["method"])
	assert.Equal(t, "/api/things", entry["path"])
	assert.EqualValues(t, 201, entry["status"])
	assert.EqualValues(t, 5, entry["bytes"])
	assert.NotEmpty(t, entry["request_id"])
	assert.Contains(t, entry, "duration_ms")
}

func TestAccessLogDefaultsStatusTo200(t *testing.T) {
	var buf bytes.Buffer
	h := httpx.AccessLog(logx.New(&buf, "info"))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	assert.Contains(t, buf.String(), `"status":200`)
}

func TestNotFound(t *testing.T) {
	rec := httptest.NewRecorder()
	httpx.NotFound().ServeHTTP(rec, httptest.NewRequest("GET", "/nope", nil))
	assert.Equal(t, 404, rec.Code)
	assert.JSONEq(t, `{"error":{"code":"NOT_FOUND","message":"route not found"}}`, rec.Body.String())
}
