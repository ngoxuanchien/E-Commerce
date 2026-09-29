package httpapi

import (
	"errors"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/services/identity/internal/domain"
)

// toAppError maps domain errors to API errors; anything else stays a 500.
func toAppError(err error) error {
	var ve *domain.ValidationError
	switch {
	case errors.As(err, &ve):
		return httpx.ValidationError(ve.Fields)
	case errors.Is(err, domain.ErrEmailTaken):
		return httpx.NewError(httpx.CodeConflict, "email is already registered")
	case errors.Is(err, domain.ErrInvalidCredentials):
		return httpx.NewError(httpx.CodeUnauthenticated, "invalid email or password")
	case isSessionError(err):
		return httpx.NewError(httpx.CodeUnauthenticated, "session is invalid or expired")
	case errors.Is(err, domain.ErrNotFound):
		return httpx.NewError(httpx.CodeUnauthenticated, "user no longer exists")
	}
	return err
}

func isSessionError(err error) bool {
	return errors.Is(err, domain.ErrInvalidRefreshToken) || errors.Is(err, domain.ErrRefreshTokenReused)
}
