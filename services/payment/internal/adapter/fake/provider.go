package fake

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ngoxuanchien/e-commerce/services/payment/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/domain"
)

// Config wires the FAKE adapter. Now, HTTPClient and Logger default when nil.
type Config struct {
	PublicBaseURL, WebhookURL, WebhookSecret string
	WebhookDelay                             time.Duration
	Now                                      func() time.Time
	HTTPClient                               *http.Client
	Logger                                   *slog.Logger
}

// Provider implements app.Provider.
type Provider struct {
	cfg   Config
	store store
	wg    sync.WaitGroup
}

var _ app.Provider = (*Provider)(nil)

// New builds the adapter over the payment database (fake_sessions, payments).
func New(pool *pgxpool.Pool, cfg Config) *Provider {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Provider{cfg: cfg, store: store{pool: pool}}
}

// Wait blocks until every scheduled webhook delivery has finished (used by tests and shutdown).
func (p *Provider) Wait() { p.wg.Wait() }

func (p *Provider) Name() string { return "fake" }

func (p *Provider) CreateSession(ctx context.Context, in app.SessionInput) (app.Session, error) {
	if err := p.store.create(ctx, in.PaymentID); err != nil {
		return app.Session{}, fmt.Errorf("fake create session: %w", err)
	}
	return app.Session{
		RedirectURL: p.cfg.PublicBaseURL + "/api/payments/fake/checkout/" + in.PaymentID,
		ProviderRef: "fake_" + in.PaymentID,
	}, nil
}

// Page returns what the hosted page shows; app.ErrNotFound when there is no session.
func (p *Provider) Page(ctx context.Context, paymentID string) (PageData, error) {
	s, err := p.store.get(ctx, paymentID)
	if err != nil {
		return PageData{}, err
	}
	return PageData{PaymentID: paymentID, Amount: s.amount, ExpiresAt: s.expires}, nil
}

// ArmHooks stores test hooks on a session that has no outcome yet, so they apply to Cancel
// and to a later Submit. It replaces previously armed hooks.
func (p *Provider) ArmHooks(ctx context.Context, paymentID string, h Hooks) error {
	if err := h.validate(); err != nil {
		return err
	}
	return p.store.arm(ctx, paymentID, h)
}

// Submit records the buyer's outcome and hooks, then schedules the webhook delivery.
// Non-empty form hooks win over armed ones; empty ones keep the armed value.
// It deliberately does not check expires_at: a late success on an old page is a scenario
// the FAKE must be able to reproduce, as with a provider that cannot cancel.
func (p *Provider) Submit(ctx context.Context, paymentID, outcome string, form Hooks) error {
	if outcome != outcomeSucceed && outcome != outcomeFail {
		return fmt.Errorf("fake: unknown outcome %q", outcome)
	}
	if err := form.validate(); err != nil {
		return err
	}
	s, err := p.store.get(ctx, paymentID)
	if err != nil {
		return err
	}
	h := s.hooks.merge(form)
	if err := p.store.submit(ctx, paymentID, outcome, h); err != nil {
		return err
	}
	p.deliver(paymentID, outcome, h)
	return nil
}

// Query reports what the session shows. A session that cannot exist is unpayable, not an outage:
// a missing row (or an id that is not a payment) answers Expired, so a create that retires this
// attempt goes on to open a new one instead of failing with ErrNotFound.
func (p *Provider) Query(ctx context.Context, pay domain.Payment) (domain.Observed, error) {
	s, err := p.store.get(ctx, pay.ID)
	if errors.Is(err, app.ErrNotFound) {
		return domain.Observed{State: domain.ObservedExpired}, nil
	}
	if err != nil {
		return domain.Observed{}, err
	}
	if s.outcome != nil {
		switch *s.outcome {
		case outcomeSucceed:
			return Event{PaymentID: pay.ID, Outcome: "succeed"}.Observed(s.amount), nil
		case outcomeFail:
			return Event{PaymentID: pay.ID, Outcome: "fail"}.Observed(s.amount), nil
		default:
			return domain.Observed{State: domain.ObservedExpired}, nil
		}
	}
	if !p.cfg.Now().Before(s.expires) {
		return domain.Observed{State: domain.ObservedExpired}, nil
	}
	return domain.Observed{State: domain.ObservedPending}, nil
}

// Cancel makes the session unpayable when it can. A session that cannot exist is already
// unpayable, so a missing row answers (false, nil) like the provider that refuses to cancel.
func (p *Provider) Cancel(ctx context.Context, pay domain.Payment) (bool, error) {
	s, err := p.store.get(ctx, pay.ID)
	if errors.Is(err, app.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if s.outcome != nil || s.hooks.Cancel == "unsupported" {
		return false, nil
	}
	return p.store.expire(ctx, pay.ID)
}

func refundRef(refundID string) string { return "fake_rf_" + refundID }

func (p *Provider) Refund(ctx context.Context, in app.RefundInput) (app.RefundObserved, error) {
	s, err := p.store.get(ctx, in.Payment.ID)
	if err != nil {
		return app.RefundObserved{}, err
	}
	if s.hooks.Refund == "fail" {
		return app.RefundObserved{State: app.RefundFailed, Reason: "fake_refund_failed"}, nil
	}
	if err := p.store.addRefunded(ctx, in.Payment.ID, in.RefundID, in.Amount); err != nil {
		return app.RefundObserved{}, fmt.Errorf("fake refund: %w", err)
	}
	return app.RefundObserved{State: app.RefundSucceeded, ProviderRefundRef: refundRef(in.RefundID)}, nil
}

func (p *Provider) QueryRefund(ctx context.Context, r app.Refund) (app.RefundObserved, error) {
	s, err := p.store.get(ctx, r.PaymentID)
	if err != nil {
		return app.RefundObserved{}, err
	}
	if s.hooks.Refund == "fail" {
		return app.RefundObserved{State: app.RefundFailed, Reason: "fake_refund_failed"}, nil
	}
	return app.RefundObserved{State: app.RefundSucceeded, ProviderRefundRef: refundRef(r.ID)}, nil
}
