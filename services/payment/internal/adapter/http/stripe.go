package httpadapter

import (
	"context"
	"io"
	"net/http"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
)

// maxStripeWebhookBody bounds a Stripe event body; Stripe events are well under this.
const maxStripeWebhookBody = 256 << 10

// StripeHandler is the Stripe callback logic (stripe.Callbacks).
type StripeHandler interface {
	Webhook(ctx context.Context, raw []byte, sigHeader string) int
	Return(ctx context.Context, sessionID string) (paymentID string)
}

// StripeCallbacks serves Stripe's webhook and the browser return from Checkout (spec §5.2).
type StripeCallbacks struct{ h StripeHandler }

var _ Routes = (*StripeCallbacks)(nil)

func NewStripeCallbacks(h StripeHandler) *StripeCallbacks { return &StripeCallbacks{h: h} }

func (c *StripeCallbacks) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/payments/stripe/webhook", c.webhook)
	mux.HandleFunc("GET /api/payments/stripe/return", c.returnURL)
}

// webhook hands the raw, unparsed body and the Stripe-Signature header to the handler, which
// verifies the signature before anything else. It answers the handler's status: 200, 400 (with the
// standard error body) or 500 so Stripe retries.
func (c *StripeCallbacks) webhook(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxStripeWebhookBody))
	if err != nil {
		writeError(w, r, httpx.ValidationError(map[string]string{"body": "could not be read (at most 256 KiB)"}))
		return
	}
	switch status := c.h.Webhook(r.Context(), raw, r.Header.Get("Stripe-Signature")); status {
	case http.StatusOK:
		w.WriteHeader(http.StatusOK)
	case http.StatusBadRequest:
		writeError(w, r, httpx.NewError(httpx.CodeValidation, "invalid Stripe-Signature"))
	default:
		writeError(w, r, httpx.NewError(httpx.CodeInternal, "webhook not processed, retry"))
	}
}

// returnURL always redirects to the result page, with the payment id only when the session names a
// known payment.
func (c *StripeCallbacks) returnURL(w http.ResponseWriter, r *http.Request) {
	id := c.h.Return(r.Context(), r.URL.Query().Get("session_id"))
	if id == "" {
		http.Redirect(w, r, "/checkout/result", http.StatusFound)
		return
	}
	http.Redirect(w, r, resultURL(id), http.StatusFound)
}
