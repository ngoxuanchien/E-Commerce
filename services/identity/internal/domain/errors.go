package domain

import "errors"

// Sentinel errors returned by the application layer and mapped by adapters.
var (
	ErrNotFound            = errors.New("not found")
	ErrEmailTaken          = errors.New("email already registered")
	ErrInvalidCredentials  = errors.New("invalid email or password")
	ErrInvalidRefreshToken = errors.New("invalid refresh token")
	ErrRefreshTokenReused  = errors.New("refresh token reused")
	ErrRoleNotGrantable    = errors.New("role cannot be granted")
)

// ValidationError lists invalid input fields (JSON field name → message).
// Messages never contain the submitted values.
type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string { return "validation failed" }
