// Package stripe is payment-svc's Stripe adapter (test mode): Checkout Sessions, a verified webhook,
// session query, cancel (sessions.expire) and refunds (spec §5.2, ADR-0008).
package stripe

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	stripego "github.com/stripe/stripe-go/v86"

	"github.com/ngoxuanchien/e-commerce/services/payment/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/domain"
)

// Config wires the Stripe adapter. Now and HTTPClient default when nil.
type Config struct {
	SecretKey, WebhookSecret, PublicBaseURL string
	APIBaseURL                              string // "" = api.stripe.com; tests point it at an httptest server
	Now                                     func() time.Time
	HTTPClient                              *http.Client
	// MaxNetworkRetries caps stripe-go's own retries of transport errors, 409 and 5xx (with the
	// request's Idempotency-Key). nil keeps the library default (2).
	MaxNetworkRetries *int64
}

const (
	providerName = "stripe"
	currencyVND  = "vnd" // zero-decimal: amounts are whole dong, never ×100
	// minSessionLifetime is Stripe's 30-minute minimum for expires_at plus one minute, so clock skew
	// and request latency never push expires_at under Stripe's floor. A shorter payment deadline is
	// enforced by Cancel (sessions.expire) from the sweeper path (spec §5.2).
	minSessionLifetime = 31 * time.Minute
	returnPath         = "/api/payments/stripe/return?session_id={CHECKOUT_SESSION_ID}"
)

// Provider implements app.Provider for Stripe Checkout.
type Provider struct {
	cfg    Config
	client *stripego.Client
}

var _ app.Provider = (*Provider)(nil)

func New(cfg Config) *Provider {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 20 * time.Second}
	}
	bc := &stripego.BackendConfig{
		HTTPClient:        cfg.HTTPClient,
		MaxNetworkRetries: cfg.MaxNetworkRetries,
		// The adapter wraps and returns every error; callers log them with context.
		LeveledLogger: &stripego.LeveledLogger{Level: stripego.LevelNull},
	}
	if cfg.APIBaseURL != "" {
		bc.URL = stripego.String(cfg.APIBaseURL)
	}
	return &Provider{cfg: cfg, client: stripego.NewClient(cfg.SecretKey, stripego.WithBackends(stripego.NewBackendsWithConfig(bc)))}
}

func (p *Provider) Name() string { return providerName }

// wrap classifies a stripe-go error: transport failures and 409/429/5xx answers are
// app.ErrProviderUnavailable (retry later); any other API error is returned as is.
func wrap(op string, err error) error {
	var se *stripego.Error
	if !errors.As(err, &se) {
		return fmt.Errorf("%w: stripe %s: %w", app.ErrProviderUnavailable, op, err)
	}
	if se.HTTPStatusCode >= 500 || se.HTTPStatusCode == http.StatusTooManyRequests || se.HTTPStatusCode == http.StatusConflict {
		return fmt.Errorf("%w: stripe %s: status %d %s: %s", app.ErrProviderUnavailable, op, se.HTTPStatusCode, se.Type, se.Msg)
	}
	return fmt.Errorf("stripe %s: status %d %s %s: %s", op, se.HTTPStatusCode, se.Type, se.Code, se.Msg)
}

func isMissing(err error) bool {
	var se *stripego.Error
	return errors.As(err, &se) && se.Code == stripego.ErrorCodeResourceMissing
}

// CreateSession opens a hosted Checkout Session for the payment (spec §5.2). The payment id is the
// Idempotency-Key, client_reference_id and metadata.payment_id.
func (p *Provider) CreateSession(ctx context.Context, in app.SessionInput) (app.Session, error) {
	expires := in.ExpiresAt
	if floor := p.cfg.Now().Add(minSessionLifetime); expires.Before(floor) {
		expires = floor
	}
	back := strings.TrimRight(p.cfg.PublicBaseURL, "/") + returnPath
	params := &stripego.CheckoutSessionCreateParams{
		Mode: stripego.String(string(stripego.CheckoutSessionModePayment)),
		LineItems: []*stripego.CheckoutSessionCreateLineItemParams{{
			PriceData: &stripego.CheckoutSessionCreateLineItemPriceDataParams{
				Currency:   stripego.String(currencyVND),
				UnitAmount: stripego.Int64(in.Amount),
				ProductData: &stripego.CheckoutSessionCreateLineItemPriceDataProductDataParams{
					Name: stripego.String("Đơn hàng " + in.CheckoutID),
				},
			},
			Quantity: stripego.Int64(1),
		}},
		ClientReferenceID: stripego.String(in.PaymentID),
		SuccessURL:        stripego.String(back),
		CancelURL:         stripego.String(back),
		ExpiresAt:         stripego.Int64(expires.Unix()),
	}
	params.AddMetadata("payment_id", in.PaymentID)
	params.AddMetadata("checkout_id", in.CheckoutID)
	params.SetIdempotencyKey(in.PaymentID)
	s, err := p.client.V1CheckoutSessions.Create(ctx, params)
	if err != nil {
		return app.Session{}, wrap("create checkout session", err)
	}
	return app.Session{RedirectURL: s.URL, ProviderRef: s.ID}, nil
}

// noSession is the state of a payment Stripe has no session for: pending until its deadline.
func (p *Provider) noSession(pay domain.Payment) domain.Observed {
	if p.cfg.Now().Before(pay.ExpiresAt) {
		return domain.Observed{State: domain.ObservedPending}
	}
	return domain.Observed{State: domain.ObservedExpired}
}

// Query retrieves the payment's Checkout Session with payment_intent expanded and maps it:
//   - complete + paid → Succeeded(amount_total, PaymentIntent id);
//   - complete + unpaid (an async payment method still settling) → Pending;
//   - expired → Expired; open → Pending;
//   - no session (never created, or unknown to Stripe) → Pending before ExpiresAt, Expired after.
func (p *Provider) Query(ctx context.Context, pay domain.Payment) (domain.Observed, error) {
	if pay.ProviderRef == "" {
		return p.noSession(pay), nil
	}
	params := &stripego.CheckoutSessionRetrieveParams{}
	params.AddExpand("payment_intent")
	s, err := p.client.V1CheckoutSessions.Retrieve(ctx, pay.ProviderRef, params)
	switch {
	case isMissing(err):
		return p.noSession(pay), nil
	case err != nil:
		return domain.Observed{}, wrap("retrieve checkout session", err)
	}
	if s.ClientReferenceID != pay.ID {
		return domain.Observed{}, fmt.Errorf("stripe query: session %s belongs to %q, not payment %s", s.ID, s.ClientReferenceID, pay.ID)
	}
	switch s.Status {
	case stripego.CheckoutSessionStatusComplete:
		if s.PaymentStatus != stripego.CheckoutSessionPaymentStatusPaid {
			return domain.Observed{State: domain.ObservedPending}, nil
		}
		if !strings.EqualFold(string(s.Currency), currencyVND) {
			return domain.Observed{}, fmt.Errorf("stripe query: session %s was paid in %q, not vnd", s.ID, s.Currency)
		}
		obs := domain.Observed{State: domain.ObservedSucceeded, Amount: s.AmountTotal}
		if s.PaymentIntent != nil {
			obs.ProviderTxnID = s.PaymentIntent.ID
		}
		if obs.ProviderTxnID == "" {
			return domain.Observed{}, fmt.Errorf("%w: stripe query: paid session %s has no payment intent", app.ErrProviderUnavailable, s.ID)
		}
		return obs, nil
	case stripego.CheckoutSessionStatusExpired:
		return domain.Observed{State: domain.ObservedExpired}, nil
	default: // open
		return domain.Observed{State: domain.ObservedPending}, nil
	}
}

// Cancel expires the Checkout Session. When Stripe refuses because the session is no longer open,
// the session is retrieved: complete → (false, nil), so the caller runs Query; already expired →
// (true, nil). A payment without a session has nothing payable, so it counts as cancelled.
func (p *Provider) Cancel(ctx context.Context, pay domain.Payment) (bool, error) {
	if pay.ProviderRef == "" {
		return true, nil
	}
	_, err := p.client.V1CheckoutSessions.Expire(ctx, pay.ProviderRef, nil)
	if err == nil || isMissing(err) {
		return true, nil
	}
	var se *stripego.Error
	if !errors.As(err, &se) || se.HTTPStatusCode != http.StatusBadRequest {
		return false, wrap("expire checkout session", err)
	}
	s, rerr := p.client.V1CheckoutSessions.Retrieve(ctx, pay.ProviderRef, nil)
	if rerr != nil {
		return false, wrap("retrieve checkout session", rerr)
	}
	switch s.Status {
	case stripego.CheckoutSessionStatusComplete:
		return false, nil
	case stripego.CheckoutSessionStatusExpired:
		return true, nil
	}
	return false, wrap("expire checkout session", err)
}

// Refund creates a refund against the payment's PaymentIntent, idempotent per refund id.
func (p *Provider) Refund(ctx context.Context, in app.RefundInput) (app.RefundObserved, error) {
	if in.Payment.ProviderTxnID == "" {
		return app.RefundObserved{}, fmt.Errorf("stripe refund: payment %s has no payment intent", in.Payment.ID)
	}
	params := &stripego.RefundCreateParams{
		PaymentIntent: stripego.String(in.Payment.ProviderTxnID),
		Amount:        stripego.Int64(in.Amount),
	}
	params.AddMetadata("refund_id", in.RefundID)
	params.AddMetadata("payment_id", in.Payment.ID)
	params.SetIdempotencyKey(in.RefundID)
	r, err := p.client.V1Refunds.Create(ctx, params)
	if err != nil {
		return app.RefundObserved{}, wrap("create refund", err)
	}
	return refundObserved(r), nil
}

// QueryRefund retrieves the refund by its Stripe id. A refund never sent to Stripe is Pending, so the
// worker calls Refund for it.
func (p *Provider) QueryRefund(ctx context.Context, r app.Refund) (app.RefundObserved, error) {
	if r.ProviderRefundRef == "" {
		return app.RefundObserved{State: app.RefundPending}, nil
	}
	got, err := p.client.V1Refunds.Retrieve(ctx, r.ProviderRefundRef, nil)
	if err != nil {
		return app.RefundObserved{}, wrap("retrieve refund", err)
	}
	return refundObserved(got), nil
}

// refundObserved maps a Stripe refund status: pending and requires_action → Pending (the reconcile
// path resolves it through QueryRefund), succeeded → Succeeded, failed and canceled → Failed.
func refundObserved(r *stripego.Refund) app.RefundObserved {
	out := app.RefundObserved{State: app.RefundPending, ProviderRefundRef: r.ID}
	switch r.Status {
	case stripego.RefundStatusSucceeded:
		out.State = app.RefundSucceeded
	case stripego.RefundStatusFailed, stripego.RefundStatusCanceled:
		out.State = app.RefundFailed
		out.Reason = "stripe_" + string(r.Status)
		if r.FailureReason != "" {
			out.Reason += ":" + string(r.FailureReason)
		}
	}
	return out
}
