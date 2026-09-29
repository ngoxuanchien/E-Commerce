package httpx_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
)

func TestStatusFor(t *testing.T) {
	cases := map[string]int{
		httpx.CodeValidation:             400,
		httpx.CodeUnauthenticated:        401,
		httpx.CodeForbidden:              403,
		httpx.CodeNotFound:               404,
		httpx.CodeConflict:               409,
		httpx.CodeOutOfStock:             409,
		httpx.CodeInsufficientFunds:      409,
		httpx.CodeInvalidStateTransition: 409,
		httpx.CodeIdempotencyKeyReused:   422,
		httpx.CodeRateLimited:            429,
		httpx.CodeInternal:               500,
		httpx.CodeUpstreamUnavailable:    503,
		httpx.CodePayloadTooLarge:        413,
		"SOMETHING_ELSE":                 500,
	}
	for code, want := range cases {
		assert.Equal(t, want, httpx.StatusFor(code), code)
	}
}

type body struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

func serveError(t *testing.T, err error) (*httptest.ResponseRecorder, body) {
	t.Helper()
	rec := httptest.NewRecorder()
	httpx.WriteError(rec, httptest.NewRequest(http.MethodGet, "/", nil), err)
	var b body
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &b))
	return rec, b
}

func TestWriteErrorAppError(t *testing.T) {
	err := httpx.NewError(httpx.CodeOutOfStock, "not enough stock").
		WithDetails(map[string]any{"shortages": []any{map[string]any{"sku_id": "s1"}}})

	rec, b := serveError(t, err)

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Equal(t, "OUT_OF_STOCK", b.Error.Code)
	assert.Equal(t, "not enough stock", b.Error.Message)
	assert.Contains(t, b.Error.Details, "shortages")
}

func TestWriteErrorWrappedAppError(t *testing.T) {
	err := fmt.Errorf("load order: %w", httpx.NewError(httpx.CodeNotFound, "order not found"))
	rec, b := serveError(t, err)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "NOT_FOUND", b.Error.Code)
}

func TestWriteErrorUnknownErrorIsInternalAndDoesNotLeak(t *testing.T) {
	rec, b := serveError(t, errors.New("pq: password=secret"))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, "INTERNAL", b.Error.Code)
	assert.Equal(t, "internal server error", b.Error.Message)
	assert.NotContains(t, rec.Body.String(), "secret")
}

func TestWriteErrorOmitsEmptyDetails(t *testing.T) {
	rec, _ := serveError(t, httpx.NewError(httpx.CodeForbidden, "no"))
	assert.NotContains(t, rec.Body.String(), "details")
}

func TestAppErrorWrapKeepsCause(t *testing.T) {
	cause := errors.New("dial tcp: refused")
	err := httpx.NewError(httpx.CodeUpstreamUnavailable, "catalog unavailable").Wrap(cause)
	assert.ErrorIs(t, err, cause)
	assert.Equal(t, 503, err.Status)
}

func TestWriteErrorFallsBackToStatusForWhenStatusIsZero(t *testing.T) {
	// An *AppError built by hand (not via NewError) may leave Status unset;
	// WriteError must still derive the right HTTP status from Code.
	err := &httpx.AppError{Code: httpx.CodeNotFound, Message: "order not found"}
	rec, b := serveError(t, err)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "NOT_FOUND", b.Error.Code)
}

func TestValidationErrorDetails(t *testing.T) {
	err := httpx.ValidationError(map[string]string{"email": "is required"})
	assert.Equal(t, 400, err.Status)
	assert.Equal(t, map[string]any{"fields": map[string]string{"email": "is required"}}, err.Details)
}
