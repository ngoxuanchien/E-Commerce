package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	paymentv1 "github.com/ngoxuanchien/e-commerce/gen/payment/v1"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/domain"
)

// ErrStillPayable means an attempt could not be cancelled and its window is still open, so the buyer
// may yet pay it. The caller (order-svc) retries later.
var ErrStillPayable = errors.New("an attempt can still succeed")

// Canceller implements CancelCheckoutPayments (spec §6).
type Canceller struct {
	store     Store
	providers Providers
	obs       *Observer
	now       func() time.Time
}

func NewCanceller(store Store, providers Providers, obs *Observer, now func() time.Time) *Canceller {
	return &Canceller{store: store, providers: providers, obs: obs, now: now}
}

// CancelCheckoutPayments makes sure no attempt of the checkout can still succeed, then reports whether
// the buyer paid. For each PENDING attempt: Cancel, Query and apply what the provider says; an attempt
// that did not succeed is expired once it is cancelled or past expires_at.
// A provider that cannot be reached gives ErrProviderUnavailable; an attempt that is not cancelled and
// still inside its window gives ErrStillPayable. An unknown checkout is NOT_PAID.
func (c *Canceller) CancelCheckoutPayments(ctx context.Context, checkoutID string) (paymentv1.Outcome, error) {
	rows, err := c.store.ListCheckoutPayments(ctx, checkoutID)
	if err != nil {
		return paymentv1.Outcome_OUTCOME_UNSPECIFIED, err
	}
	var first error
	for _, p := range rows {
		if p.Status != domain.StatusPending {
			continue
		}
		if err := c.settle(ctx, p); err != nil && first == nil {
			first = err
		}
	}
	if first != nil {
		return paymentv1.Outcome_OUTCOME_UNSPECIFIED, first
	}
	rows, err = c.store.ListCheckoutPayments(ctx, checkoutID)
	if err != nil {
		return paymentv1.Outcome_OUTCOME_UNSPECIFIED, err
	}
	for _, p := range rows {
		if p.Captured {
			return paymentv1.Outcome_OUTCOME_PAID, nil
		}
	}
	return paymentv1.Outcome_OUTCOME_NOT_PAID, nil
}

func (c *Canceller) settle(ctx context.Context, p domain.Payment) error {
	prov, ok := c.providers.ByName(p.Provider)
	if !ok {
		return fmt.Errorf("%w: no adapter for provider %q", ErrProviderUnavailable, p.Provider)
	}
	cancelled, err := cancelAndObserve(ctx, c.obs, prov, p)
	if err != nil {
		return fmt.Errorf("payment %s: %w", p.ID, err)
	}
	cur, err := c.store.GetPayment(ctx, p.ID)
	if err != nil {
		return err
	}
	if cur.Status != domain.StatusPending {
		return nil // succeeded, failed or already expired: nothing left to wait for
	}
	if !cancelled && c.now().Before(cur.ExpiresAt) {
		return fmt.Errorf("payment %s: %w", p.ID, ErrStillPayable)
	}
	_, err = c.obs.Expire(ctx, p.ID)
	return err
}

// cancelAndObserve asks the provider to cancel, then re-queries and applies the observation.
// Every provider error is reported as ErrProviderUnavailable.
func cancelAndObserve(ctx context.Context, obs *Observer, prov Provider, p domain.Payment) (bool, error) {
	callCtx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()
	cancelled, err := prov.Cancel(callCtx, p)
	if err != nil {
		return false, unavailable(err)
	}
	if err := queryAndApply(callCtx, obs, prov, p); err != nil {
		return false, err
	}
	return cancelled, nil
}

// queryAndApply queries the provider and applies the observation (event key "").
func queryAndApply(ctx context.Context, obs *Observer, prov Provider, p domain.Payment) error {
	o, err := prov.Query(ctx, p)
	if err != nil {
		return unavailable(err)
	}
	_, err = obs.ApplyObserved(ctx, prov.Name(), "", p.ID, o)
	return err
}

func unavailable(err error) error {
	if errors.Is(err, ErrProviderUnavailable) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrProviderUnavailable, err)
}
