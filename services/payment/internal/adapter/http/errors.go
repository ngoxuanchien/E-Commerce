package httpadapter

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/pkg/idemkey"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/domain"
)

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	httpx.WriteError(w, r, toAppError(err))
}

// toAppError maps use-case errors to API errors (WS0 §3 codes).
func toAppError(err error) *httpx.AppError {
	var (
		ae *httpx.AppError
		st *app.StateError
	)
	switch {
	case errors.As(err, &ae):
		if ae.Status == 0 {
			ae = httpx.NewError(ae.Code, ae.Message).WithDetails(ae.Details)
		}
		return ae
	case errors.Is(err, app.ErrNotFound):
		return httpx.NewError(httpx.CodeNotFound, "not found")
	case errors.As(err, &st):
		return httpx.NewError(httpx.CodeInvalidStateTransition, "payment cannot be started: "+st.Reason).
			WithDetails(map[string]any{"reason": st.Reason})
	case errors.Is(err, app.ErrKeyReused):
		return httpx.NewError(httpx.CodeIdempotencyKeyReused, "Idempotency-Key was already used with a different request")
	case errors.Is(err, app.ErrKeyInProgress), errors.Is(err, idemkey.ErrClaimLost):
		return httpx.NewError(httpx.CodeConflict, "a request with this Idempotency-Key is in progress")
	case errors.Is(err, app.ErrOrdersUnavailable):
		return httpx.NewError(httpx.CodeUpstreamUnavailable, "order service is unavailable, retry shortly").Wrap(err)
	case errors.Is(err, app.ErrProviderUnavailable):
		return httpx.NewError(httpx.CodeUpstreamUnavailable, "payment provider is unavailable, retry shortly").Wrap(err)
	default:
		return httpx.NewError(httpx.CodeInternal, "internal server error").Wrap(err)
	}
}

type errorEnvelope struct {
	Error errorPayload `json:"error"`
}

type errorPayload struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// encodePayment is the app.Encoder for CreatePayment: 201 with the payment, or the standard
// error body for a business rejection (a StateError or an unknown or foreign checkout).
func encodePayment(p domain.Payment, err error) idemkey.Response {
	if err != nil {
		ae := toAppError(err)
		body, _ := json.Marshal(errorEnvelope{Error: errorPayload{Code: ae.Code, Message: ae.Message, Details: ae.Details}})
		return idemkey.Response{Status: ae.Status, Body: body}
	}
	body, _ := json.Marshal(toPaymentDTO(p))
	return idemkey.Response{Status: http.StatusCreated, Body: body}
}
