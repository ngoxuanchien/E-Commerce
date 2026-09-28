package httpapi

import (
	"errors"
	"net/http"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
)

// writeError maps domain errors to the REST error model (WS0 §3).
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	var (
		ve *domain.ValidationError
		ce *domain.ConflictError
	)
	switch {
	case errors.As(err, &ve):
		err = httpx.ValidationError(ve.Fields)
	case errors.As(err, &ce):
		err = httpx.NewError(httpx.CodeConflict, ce.Message)
	case errors.Is(err, domain.ErrNotFound):
		err = httpx.NewError(httpx.CodeNotFound, "not found")
	case errors.Is(err, domain.ErrNoShop):
		err = httpx.NewError(httpx.CodeForbidden, "open a shop first")
	case errors.Is(err, domain.ErrUpstreamUnavailable):
		err = httpx.NewError(httpx.CodeUpstreamUnavailable, "identity service unavailable, please retry").Wrap(err)
	}
	httpx.WriteError(w, r, err)
}
