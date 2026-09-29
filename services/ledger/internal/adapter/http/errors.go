package httpadapter

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/app"
)

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	httpx.WriteError(w, r, toAppError(err))
}

// toAppError maps use-case errors to API errors. Unknown errors pass
// through, and httpx turns them into a logged 500.
func toAppError(err error) error {
	var (
		ae  *httpx.AppError
		bm  *app.BelowMinimumError
		ife *app.InsufficientFundsError
	)
	switch {
	case errors.As(err, &ae):
		return ae
	case errors.As(err, &bm):
		return httpx.ValidationError(map[string]string{"amount": fmt.Sprintf("must be at least %d", bm.Min)})
	case errors.As(err, &ife):
		return httpx.NewError(httpx.CodeInsufficientFunds, "insufficient funds").
			WithDetails(map[string]any{"available": ife.Available})
	case errors.Is(err, app.ErrIdempotencyKeyReused):
		return httpx.NewError(httpx.CodeIdempotencyKeyReused, "Idempotency-Key was used with a different request")
	case errors.Is(err, app.ErrIdempotencyInProgress):
		return httpx.NewError(httpx.CodeConflict, "a request with this Idempotency-Key is still in progress")
	case errors.Is(err, app.ErrInvalidCursor):
		return httpx.ValidationError(map[string]string{"cursor": "is invalid"})
	}
	return err
}

type errorEnvelope struct {
	Error errorPayload `json:"error"`
}

type errorPayload struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// renderError renders a deterministic rejection for the idempotency store.
func renderError(err error) app.Response {
	var ae *httpx.AppError
	if !errors.As(toAppError(err), &ae) {
		ae = httpx.NewError(httpx.CodeInternal, "internal server error")
	}
	body, _ := json.Marshal(errorEnvelope{Error: errorPayload{Code: ae.Code, Message: ae.Message, Details: ae.Details}})
	return app.Response{Status: httpx.StatusFor(ae.Code), Body: body}
}
