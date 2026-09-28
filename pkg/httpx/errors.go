// Package httpx holds the HTTP conventions shared by all services: the error
// model, JSON encoding/decoding with validation, and common middleware.
package httpx

import (
	"errors"
	"log/slog"
	"net/http"
)

// Error codes (WS0 §3).
const (
	CodeValidation             = "VALIDATION_ERROR"
	CodeUnauthenticated        = "UNAUTHENTICATED"
	CodeForbidden              = "FORBIDDEN"
	CodeNotFound               = "NOT_FOUND"
	CodeConflict               = "CONFLICT"
	CodeOutOfStock             = "OUT_OF_STOCK"
	CodeInvalidStateTransition = "INVALID_STATE_TRANSITION"
	CodeIdempotencyKeyReused   = "IDEMPOTENCY_KEY_REUSED"
	CodeRateLimited            = "RATE_LIMITED"
	CodeInternal               = "INTERNAL"
	CodeUpstreamUnavailable    = "UPSTREAM_UNAVAILABLE"
)

var codeStatus = map[string]int{
	CodeValidation:             http.StatusBadRequest,
	CodeUnauthenticated:        http.StatusUnauthorized,
	CodeForbidden:              http.StatusForbidden,
	CodeNotFound:               http.StatusNotFound,
	CodeConflict:               http.StatusConflict,
	CodeOutOfStock:             http.StatusConflict,
	CodeInvalidStateTransition: http.StatusConflict,
	CodeIdempotencyKeyReused:   http.StatusUnprocessableEntity,
	CodeRateLimited:            http.StatusTooManyRequests,
	CodeInternal:               http.StatusInternalServerError,
	CodeUpstreamUnavailable:    http.StatusServiceUnavailable,
}

// StatusFor maps an error code to its HTTP status; unknown codes map to 500.
func StatusFor(code string) int {
	if s, ok := codeStatus[code]; ok {
		return s
	}
	return http.StatusInternalServerError
}

// AppError is an error that is safe to return to API clients.
// Err is the internal cause; it is logged but never serialized.
type AppError struct {
	Code    string
	Status  int
	Message string
	Details map[string]any
	Err     error
}

// NewError builds an AppError whose status is derived from code.
func NewError(code, message string) *AppError {
	return &AppError{Code: code, Status: StatusFor(code), Message: message}
}

// ValidationError builds a VALIDATION_ERROR whose details are {"fields": {field: message}}.
func ValidationError(fields map[string]string) *AppError {
	return NewError(CodeValidation, "request validation failed").WithDetails(map[string]any{"fields": fields})
}

func (e *AppError) Error() string {
	if e.Err != nil {
		return e.Code + ": " + e.Message + ": " + e.Err.Error()
	}
	return e.Code + ": " + e.Message
}

func (e *AppError) Unwrap() error { return e.Err }

// WithDetails returns a copy of e with details set.
func (e *AppError) WithDetails(details map[string]any) *AppError {
	c := *e
	c.Details = details
	return &c
}

// Wrap returns a copy of e with the internal cause set.
func (e *AppError) Wrap(err error) *AppError {
	c := *e
	c.Err = err
	return &c
}

type errorBody struct {
	Error errorPayload `json:"error"`
}

type errorPayload struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// WriteError writes err in the standard error format. Errors that are not
// *AppError become a generic 500 INTERNAL; their text is logged, never returned.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var ae *AppError
	if !errors.As(err, &ae) {
		slog.ErrorContext(r.Context(), "unhandled error", "error", err)
		ae = NewError(CodeInternal, "internal server error")
	} else if ae.Status >= http.StatusInternalServerError {
		slog.ErrorContext(r.Context(), "server error", "code", ae.Code, "error", err)
	}
	WriteJSON(w, ae.Status, errorBody{Error: errorPayload{Code: ae.Code, Message: ae.Message, Details: ae.Details}})
}
