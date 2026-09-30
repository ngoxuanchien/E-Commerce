package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/domain"
)

// Refunds applies the OrderCancelled half of the refund-due rule (spec §3).
type Refunds struct {
	store Store
	now   func() time.Time
}

func NewRefunds(store Store, now func() time.Time) *Refunds {
	return &Refunds{store: store, now: now}
}

// OrderCancelled handles one order.events record in one transaction: dedupe by eventID, then record
// the cancellation and create the order's refund if its allocation is already captured.
// P1-era events (no payment_method or checkout_id) and COD events are only marked processed.
//
// The checkout's payment rows are locked before the cancellation is recorded, exactly as the capture
// path does. At READ COMMITTED a concurrent capture and cancellation would otherwise each miss the
// other's write and no refund would be created.
func (r *Refunds) OrderCancelled(ctx context.Context, eventID string, e event.OrderCancelled, at time.Time) error {
	return r.store.InTx(ctx, func(tx Tx) error {
		first, err := tx.MarkProcessed(ctx, eventID)
		if err != nil {
			return err
		}
		if !first {
			return nil
		}
		if e.PaymentMethod == "" || e.PaymentMethod == event.PaymentMethodCOD || e.CheckoutID == "" {
			return nil
		}
		if _, err := tx.LockCheckoutPayments(ctx, e.CheckoutID); err != nil {
			return err
		}
		if _, err := tx.RecordCancellation(ctx, e.OrderID, e.CheckoutID, at); err != nil {
			return err
		}
		p, a, ok, err := tx.CapturedAllocationFor(ctx, e.OrderID)
		if err != nil {
			return err
		}
		if !ok || !domain.RefundDue(p.Captured, true) {
			return nil
		}
		orderID := a.OrderID
		_, err = tx.InsertRefund(ctx, newRefund(p, &orderID, a.Amount, r.now()))
		return err
	})
}

// Backoff is the wait after the given number of failed attempts: 10 s · 2^(attempts−1), capped at 10 min.
func Backoff(attempts int) time.Duration {
	const base, limit = 10 * time.Second, 10 * time.Minute
	if attempts < 1 {
		attempts = 1
	}
	if attempts > 7 {
		return limit
	}
	return min(base<<(attempts-1), limit)
}

// PendingBackoff is the wait before re-checking a refund the provider reported as pending:
// half its age, clamped to [10 s, 10 min].
func PendingBackoff(age time.Duration) time.Duration {
	return min(max(age/2, 10*time.Second), 10*time.Minute)
}

const (
	refundBatch     = 20
	providerTimeout = 15 * time.Second
	pendingAlertAge = 24 * time.Hour
	statusPending   = "PENDING"
	statusSucceeded = "SUCCEEDED"
	statusFailed    = "FAILED"
)

// RefundHint names one refund reported by a provider event.
type RefundHint struct {
	ProviderRefundRef string
	RefundID          string // payment-svc refund id, "" when unknown
}

// RefundWorker drives PENDING refunds to a terminal state.
type RefundWorker struct {
	store       Store
	providers   Providers
	maxAttempts int
	now         func() time.Time
	logger      *slog.Logger
}

func NewRefundWorker(store Store, providers Providers, maxAttempts int, now func() time.Time, logger *slog.Logger) *RefundWorker {
	if logger == nil {
		logger = slog.Default()
	}
	return &RefundWorker{store: store, providers: providers, maxAttempts: maxAttempts, now: now, logger: logger}
}

// RunOnce settles up to refundBatch due refunds and returns how many it handled. Each refund runs in
// its own transaction (claimed with SKIP LOCKED), so an error on one never rolls back a refund a
// provider already executed and the worker committed; the first error stops the run.
// The row lock is held for the provider call, which is bounded by providerTimeout.
func (w *RefundWorker) RunOnce(ctx context.Context) (int, error) {
	n := 0
	for n < refundBatch {
		found := false
		err := w.store.InTx(ctx, func(tx Tx) error {
			found = false
			now := w.now()
			rows, err := tx.ClaimDueRefunds(ctx, now, 1)
			if err != nil || len(rows) == 0 {
				return err
			}
			found = true
			return w.process(ctx, tx, rows[0], "", now, false)
		})
		if err != nil {
			return n, fmt.Errorf("refund worker: %w", err)
		}
		if !found {
			break
		}
		n++
	}
	return n, nil
}

// Run calls RunOnce every interval until ctx ends.
func (w *RefundWorker) Run(ctx context.Context, every time.Duration) error {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if n, err := w.RunOnce(ctx); err != nil {
			w.logger.ErrorContext(ctx, "refund worker run failed", "error", err)
		} else if n > 0 {
			w.logger.InfoContext(ctx, "refunds processed", "count", n)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

// ApplyRefundObserved is the provider-hint path: it finds the refund by (provider, ref), falling back
// to the payment-svc refund id, re-checks it with QueryRefund and settles it. An unknown or already
// settled refund is not an error (nil), so a provider webhook is never retried for it. When the row
// has no provider ref yet, the hint's (signed) ref is adopted. A provider that cannot be reached is
// returned as an error, so the webhook answers 5xx and the provider retries.
func (w *RefundWorker) ApplyRefundObserved(ctx context.Context, provider string, in RefundHint) error {
	var id string
	if in.ProviderRefundRef != "" {
		rf, err := w.store.GetRefundByProviderRef(ctx, provider, in.ProviderRefundRef)
		switch {
		case err == nil:
			id = rf.ID
		case !errors.Is(err, ErrNotFound):
			return err
		}
	}
	if id == "" {
		id = in.RefundID
	}
	if id == "" {
		return nil
	}
	return w.store.InTx(ctx, func(tx Tx) error {
		rf, err := tx.LockRefund(ctx, id)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if rf.Status != statusPending || rf.Provider != provider {
			return nil
		}
		return w.process(ctx, tx, rf, in.ProviderRefundRef, w.now(), true)
	})
}

// process asks the provider about one locked refund and records the outcome. On the hint path it
// always calls QueryRefund, with adopt as the ref if the row has none.
func (w *RefundWorker) process(ctx context.Context, tx Tx, rf Refund, adopt string, now time.Time, hint bool) error {
	if hint && rf.ProviderRefundRef == "" {
		rf.ProviderRefundRef = adopt
	}
	prov, ok := w.providers.ByName(rf.Provider)
	if !ok {
		return w.settle(ctx, tx, rf, false, RefundObserved{}, fmt.Errorf("no adapter for provider %q", rf.Provider), now)
	}
	accepted := rf.ProviderRefundRef != ""
	callCtx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()
	var (
		obs RefundObserved
		err error
	)
	if !accepted && !hint {
		p, gerr := w.store.GetPayment(ctx, rf.PaymentID)
		if gerr != nil {
			return gerr
		}
		obs, err = prov.Refund(callCtx, RefundInput{RefundID: rf.ID, Payment: p, Amount: rf.Amount})
	} else {
		obs, err = prov.QueryRefund(callCtx, rf)
	}
	if hint && err != nil {
		return fmt.Errorf("query refund %s: %w", rf.ID, err)
	}
	return w.settle(ctx, tx, rf, accepted, obs, err, now)
}

// settle records what the provider said. accepted means the provider already holds the refund (its
// ref is stored): errors then only delay the next poll, and a reported failure is final.
func (w *RefundWorker) settle(ctx context.Context, tx Tx, rf Refund, accepted bool, obs RefundObserved, callErr error, now time.Time) error {
	if obs.ProviderRefundRef != "" {
		rf.ProviderRefundRef = obs.ProviderRefundRef
	}
	age := now.Sub(rf.CreatedAt)
	failure := ""
	switch {
	case callErr != nil:
		if !errors.Is(callErr, ErrProviderUnavailable) {
			w.logger.ErrorContext(ctx, "refund provider call failed", "refund_id", rf.ID, "error", callErr)
		}
		if accepted {
			return w.stillPending(ctx, tx, rf, age, now)
		}
		failure = callErr.Error()
	case obs.State == RefundSucceeded:
		rf.Status, rf.LastError = statusSucceeded, ""
		if err := tx.UpdateRefund(ctx, rf); err != nil {
			return err
		}
		return tx.Publish(ctx, rf.CheckoutID, paymentRefunded(rf), now)
	case obs.State == RefundPending:
		return w.stillPending(ctx, tx, rf, age, now)
	default:
		failure = obs.Reason
		if failure == "" {
			failure = "refund failed"
		}
		if accepted {
			return w.fail(ctx, tx, rf, failure, now)
		}
	}
	rf.Attempts++
	rf.LastError = failure
	rf.NextAttemptAt = now.Add(Backoff(rf.Attempts))
	if rf.Attempts >= w.maxAttempts {
		return w.fail(ctx, tx, rf, failure, now)
	}
	return tx.UpdateRefund(ctx, rf)
}

// stillPending reschedules a refund the provider holds but has not settled, by the refund's age.
// It never becomes FAILED by itself; past pendingAlertAge every poll logs an error.
func (w *RefundWorker) stillPending(ctx context.Context, tx Tx, rf Refund, age time.Duration, now time.Time) error {
	if age >= pendingAlertAge {
		w.logger.ErrorContext(ctx, "refund still pending at the provider", "refund_id", rf.ID, "provider", rf.Provider,
			"provider_refund_ref", rf.ProviderRefundRef, "age", age.String())
	}
	rf.NextAttemptAt = now.Add(PendingBackoff(age))
	return tx.UpdateRefund(ctx, rf)
}

func (w *RefundWorker) fail(ctx context.Context, tx Tx, rf Refund, reason string, now time.Time) error {
	rf.Status, rf.LastError = statusFailed, reason
	if err := tx.UpdateRefund(ctx, rf); err != nil {
		return err
	}
	return tx.Publish(ctx, rf.CheckoutID, refundFailed(rf, reason), now)
}
