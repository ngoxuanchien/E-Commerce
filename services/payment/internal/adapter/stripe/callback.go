package stripe

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	stripego "github.com/stripe/stripe-go/v86"
	"github.com/stripe/stripe-go/v86/webhook"

	"github.com/ngoxuanchien/e-commerce/services/payment/internal/app"
)

// returnQueryTimeout bounds the session query made while the buyer's browser waits on the return.
const returnQueryTimeout = 5 * time.Second

// Callbacks holds the webhook and browser-return logic; the http adapter serves it.
type Callbacks struct {
	provider *Provider
	store    app.Store
	observer *app.Observer
	logger   *slog.Logger

	// RefundHint is the Task 8 hook for charge.refunded and refund.updated: it is called once per
	// Stripe refund id (re_…) the event names, and should re-check that refund (QueryRefund). nil
	// acknowledges refund events with 200 and does nothing. An error answers 500, so Stripe retries.
	RefundHint func(ctx context.Context, providerRefundRef string) error
}

// NewCallbacks wires Callbacks; logger defaults to slog.Default().
func NewCallbacks(p *Provider, store app.Store, observer *app.Observer, logger *slog.Logger) Callbacks {
	if logger == nil {
		logger = slog.Default()
	}
	return Callbacks{provider: p, store: store, observer: observer, logger: logger}
}

// Webhook verifies and handles one Stripe event and returns the HTTP status to answer (spec §5.2):
//   - the Stripe-Signature over the raw body is checked first (300 s tolerance); any failure → 400;
//   - checkout.session.* events are only hints: metadata.payment_id → Query → ApplyObserved, with
//     event.id as the dedupe key, so delivery order never matters;
//   - charge.refunded and refund.updated go to RefundHint;
//   - an unknown payment or an ignored type → 200; an internal or provider error → 500 (Stripe retries).
//
// The API-version check of ConstructEvent is disabled: the handler reads only id, type,
// data.object.id, data.object.metadata and refund ids, which are stable across API versions, and
// re-reads the session through the pinned client. Without this, events from an endpoint left on the
// dashboard's default API version would all be rejected.
func (c Callbacks) Webhook(ctx context.Context, raw []byte, sigHeader string) int {
	secret := c.provider.cfg.WebhookSecret
	if secret == "" { // an empty key would let anyone sign
		c.logger.ErrorContext(ctx, "stripe webhook: STRIPE_WEBHOOK_SECRET is not set; rejecting")
		return http.StatusBadRequest
	}
	ev, err := webhook.ConstructEventWithOptions(raw, sigHeader, secret,
		webhook.ConstructEventOptions{IgnoreAPIVersionMismatch: true})
	if err != nil {
		c.logger.WarnContext(ctx, "stripe webhook: rejected", "error", err)
		return http.StatusBadRequest
	}
	switch ev.Type {
	case stripego.EventTypeCheckoutSessionCompleted, stripego.EventTypeCheckoutSessionAsyncPaymentSucceeded,
		stripego.EventTypeCheckoutSessionAsyncPaymentFailed, stripego.EventTypeCheckoutSessionExpired:
		return c.sessionEvent(ctx, ev)
	case stripego.EventTypeChargeRefunded, stripego.EventTypeRefundUpdated:
		return c.refundEvent(ctx, ev)
	}
	return http.StatusOK
}

func (c Callbacks) sessionEvent(ctx context.Context, ev stripego.Event) int {
	var obj struct {
		ID       string            `json:"id"`
		Metadata map[string]string `json:"metadata"`
	}
	if ev.Data == nil || json.Unmarshal(ev.Data.Raw, &obj) != nil {
		c.logger.ErrorContext(ctx, "stripe webhook: unreadable session object", "event_id", ev.ID)
		return http.StatusOK // a signed event that does not parse will not parse on a retry either
	}
	paymentID := obj.Metadata["payment_id"]
	if _, err := uuid.Parse(paymentID); err != nil {
		c.logger.WarnContext(ctx, "stripe webhook: session without a payment id", "event_id", ev.ID, "session_id", obj.ID)
		return http.StatusOK
	}
	p, err := c.store.GetPayment(ctx, paymentID)
	switch {
	case errors.Is(err, app.ErrNotFound):
		return http.StatusOK
	case err != nil:
		c.logger.ErrorContext(ctx, "stripe webhook: load payment", "payment_id", paymentID, "error", err)
		return http.StatusInternalServerError
	}
	if p.Provider != providerName || p.ProviderRef != obj.ID {
		c.logger.WarnContext(ctx, "stripe webhook: session does not match the payment",
			"event_id", ev.ID, "payment_id", p.ID, "session_id", obj.ID, "provider_ref", p.ProviderRef)
		return http.StatusOK
	}
	obs, err := c.provider.Query(ctx, p)
	if err != nil {
		c.logger.ErrorContext(ctx, "stripe webhook: query", "payment_id", p.ID, "error", err)
		return http.StatusInternalServerError
	}
	if _, err := c.observer.ApplyObserved(ctx, providerName, ev.ID, p.ID, obs); err != nil {
		c.logger.ErrorContext(ctx, "stripe webhook: apply", "payment_id", p.ID, "error", err)
		return http.StatusInternalServerError
	}
	return http.StatusOK
}

// refundEvent passes the refund ids an event names to RefundHint: the refund itself for
// refund.updated, and the charge's embedded refunds list (when present) for charge.refunded.
func (c Callbacks) refundEvent(ctx context.Context, ev stripego.Event) int {
	if c.RefundHint == nil || ev.Data == nil {
		return http.StatusOK
	}
	var obj struct {
		ID      string `json:"id"`
		Refunds *struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		} `json:"refunds"`
	}
	if json.Unmarshal(ev.Data.Raw, &obj) != nil {
		c.logger.ErrorContext(ctx, "stripe webhook: unreadable refund object", "event_id", ev.ID)
		return http.StatusOK
	}
	var refs []string
	if ev.Type == stripego.EventTypeRefundUpdated {
		refs = append(refs, obj.ID)
	} else if obj.Refunds != nil {
		for _, r := range obj.Refunds.Data {
			refs = append(refs, r.ID)
		}
	}
	for _, ref := range refs {
		if ref == "" {
			continue
		}
		if err := c.RefundHint(ctx, ref); err != nil {
			c.logger.ErrorContext(ctx, "stripe webhook: refund hint", "event_id", ev.ID, "refund_ref", ref, "error", err)
			return http.StatusInternalServerError
		}
	}
	return http.StatusOK
}

// Return handles the buyer's browser coming back from Checkout. It syncs the payment from the
// session (never from the URL, ADR-0008) and returns the payment id for the result page, or "" when
// the session is unknown. Query and apply errors are logged, not surfaced: the webhook and the
// reconciler catch up.
func (c Callbacks) Return(ctx context.Context, sessionID string) (paymentID string) {
	if sessionID == "" {
		return ""
	}
	p, err := c.store.GetPaymentByProviderRef(ctx, providerName, sessionID)
	if err != nil {
		if !errors.Is(err, app.ErrNotFound) {
			c.logger.ErrorContext(ctx, "stripe return: load payment", "session_id", sessionID, "error", err)
		}
		return ""
	}
	if p.Status.Final() {
		return p.ID
	}
	qctx, cancel := context.WithTimeout(ctx, returnQueryTimeout)
	defer cancel()
	obs, err := c.provider.Query(qctx, p)
	if err != nil {
		c.logger.ErrorContext(ctx, "stripe return: query", "payment_id", p.ID, "error", err)
		return p.ID
	}
	if _, err := c.observer.ApplyObserved(ctx, providerName, "", p.ID, obs); err != nil {
		c.logger.ErrorContext(ctx, "stripe return: apply", "payment_id", p.ID, "error", err)
	}
	return p.ID
}
