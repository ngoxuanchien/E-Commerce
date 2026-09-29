package app

import (
	"context"
	"errors"
	"time"

	"github.com/ngoxuanchien/e-commerce/services/payment/internal/domain"
)

// Provider is the port every payment adapter implements (spec §4).
type Provider interface {
	Name() string
	CreateSession(ctx context.Context, in SessionInput) (Session, error)
	Query(ctx context.Context, p domain.Payment) (domain.Observed, error)
	Cancel(ctx context.Context, p domain.Payment) (cancelled bool, err error)
	Refund(ctx context.Context, in RefundInput) (RefundObserved, error) // idempotent per RefundID
	QueryRefund(ctx context.Context, r Refund) (RefundObserved, error)
}

// SessionInput is what an adapter needs to open a payment session.
type SessionInput struct {
	PaymentID, CheckoutID            string
	Amount                           int64
	ExpiresAt                        time.Time
	ReturnURL, Description, ClientIP string
}

// Session is the buyer redirect and the provider's reference for the payment.
type Session struct{ RedirectURL, ProviderRef string }

// RefundInput asks a provider to refund Amount of Payment. RefundID makes the call idempotent.
type RefundInput struct {
	RefundID string
	Payment  domain.Payment
	Amount   int64
}

// RefundState is the provider-side state of a refund.
type RefundState int

const (
	RefundPending RefundState = iota + 1
	RefundSucceeded
	RefundFailed
)

// RefundObserved is what a provider reported about a refund.
type RefundObserved struct {
	State             RefundState
	ProviderRefundRef string
	Reason            string
}

// ErrProviderUnavailable wraps transport errors and 5xx answers from a provider.
var ErrProviderUnavailable = errors.New("provider unavailable")

// Providers resolves adapters by payment method or by provider name.
type Providers struct {
	byMethod map[string]Provider
	byName   map[string]Provider
}

// NewProviders wires the VNPAY and STRIPE methods; either may be the FAKE adapter.
func NewProviders(forVNPay, forStripe Provider) Providers {
	p := Providers{
		byMethod: map[string]Provider{"VNPAY": forVNPay, "STRIPE": forStripe},
		byName:   map[string]Provider{},
	}
	for _, a := range []Provider{forVNPay, forStripe} {
		if a != nil {
			p.byName[a.Name()] = a
		}
	}
	return p
}

// ForMethod returns the adapter for a payment method (VNPAY | STRIPE).
func (p Providers) ForMethod(method string) (Provider, bool) {
	a, ok := p.byMethod[method]
	return a, ok && a != nil
}

// ByName returns the adapter with the given Name (vnpay | stripe | fake).
func (p Providers) ByName(name string) (Provider, bool) {
	a, ok := p.byName[name]
	return a, ok
}
