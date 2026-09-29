package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/services/payment/internal/domain"
)

// Observer applies provider observations to payments.
type Observer struct {
	store  Store
	now    func() time.Time
	logger *slog.Logger
}

func NewObserver(store Store, now func() time.Time, logger *slog.Logger) *Observer {
	return &Observer{store: store, now: now, logger: logger}
}

// Outcome is the result of ApplyObserved. Payment is the row after the call.
type Outcome struct {
	Payment                   domain.Payment
	Unchanged, AmountMismatch bool
}

func findPayment(rows []domain.Payment, id string) (domain.Payment, bool) {
	for _, r := range rows {
		if r.ID == id {
			return r, true
		}
	}
	return domain.Payment{}, false
}

// ApplyObserved is the only way payment state changes after creation, in one transaction:
// dedupe by eventKey, lock the checkout's payments, Apply, then write state, outbox and refunds.
func (o *Observer) ApplyObserved(ctx context.Context, provider, eventKey, paymentID string, obs domain.Observed) (Outcome, error) {
	pre, err := o.store.GetPayment(ctx, paymentID)
	if err != nil {
		return Outcome{}, err
	}
	var out Outcome
	err = o.store.InTx(ctx, func(tx Tx) error {
		out = Outcome{}
		now := o.now()
		if eventKey != "" {
			first, err := tx.InsertProviderEvent(ctx, provider, eventKey, now)
			if err != nil {
				return err
			}
			if !first {
				out = Outcome{Payment: pre, Unchanged: true}
				return nil
			}
		}
		rows, err := tx.LockCheckoutPayments(ctx, pre.CheckoutID)
		if err != nil {
			return err
		}
		cur, ok := findPayment(rows, paymentID)
		if !ok {
			return ErrNotFound
		}
		next, eff := domain.Apply(cur, obs)
		out.Payment = cur
		switch eff {
		case domain.EffectAmountMismatch:
			o.logger.ErrorContext(ctx, "provider amount mismatch", "payment_id", cur.ID, "expected", cur.Amount, "got", obs.Amount)
			out.Unchanged, out.AmountMismatch = true, true
			return nil
		case domain.EffectNone:
			if next.Status == cur.Status {
				out.Unchanged = true
				return nil
			}
			next.UpdatedAt = now
			if err := tx.UpdatePayment(ctx, next); err != nil {
				return err
			}
			out.Payment = next
		case domain.EffectFailed:
			next.UpdatedAt = now
			if err := tx.UpdatePayment(ctx, next); err != nil {
				return err
			}
			if err := tx.Publish(ctx, next.CheckoutID, paymentFailed(next), now); err != nil {
				return err
			}
			out.Payment = next
		case domain.EffectSucceeded:
			p, err := o.capture(ctx, tx, rows, next, now)
			if err != nil {
				return err
			}
			out.Payment = p
		}
		return nil
	})
	if err != nil {
		return Outcome{}, err
	}
	return out, nil
}

// capture records a succeeded payment as the checkout's captured payment or as a DUPLICATE.
func (o *Observer) capture(ctx context.Context, tx Tx, rows []domain.Payment, p domain.Payment, now time.Time) (domain.Payment, error) {
	other := false
	for _, r := range rows {
		if r.ID != p.ID && r.Captured {
			other = true
		}
	}
	p.UpdatedAt = now
	if domain.Capture(other) == domain.CaptureAsDuplicate {
		p.Status, p.Captured = domain.StatusDuplicate, false
		if err := tx.UpdatePayment(ctx, p); err != nil {
			return p, err
		}
		if err := tx.Publish(ctx, p.CheckoutID, paymentSucceeded(p, nil), now); err != nil {
			return p, err
		}
		if _, err := tx.InsertRefund(ctx, newRefund(p, nil, p.Amount, now)); err != nil {
			return p, err
		}
		return p, nil
	}
	p.Captured = true
	if err := tx.UpdatePayment(ctx, p); err != nil {
		return p, err
	}
	allocs, err := tx.Allocations(ctx, p.ID)
	if err != nil {
		return p, err
	}
	if err := tx.Publish(ctx, p.CheckoutID, paymentSucceeded(p, allocs), now); err != nil {
		return p, err
	}
	for _, a := range allocs {
		cancelled, err := tx.IsCancelled(ctx, a.OrderID)
		if err != nil {
			return p, err
		}
		if domain.RefundDue(p.Captured, cancelled) {
			orderID := a.OrderID
			if _, err := tx.InsertRefund(ctx, newRefund(p, &orderID, a.Amount, now)); err != nil {
				return p, err
			}
		}
	}
	return p, nil
}

func newRefund(p domain.Payment, orderID *string, amount int64, now time.Time) Refund {
	return Refund{
		ID: uuid.Must(uuid.NewV7()).String(), PaymentID: p.ID, CheckoutID: p.CheckoutID, Provider: p.Provider,
		OrderID: orderID, Amount: amount, Status: "PENDING", NextAttemptAt: now,
	}
}

// Expire marks a still-PENDING payment EXPIRED. Any other status is returned unchanged.
func (o *Observer) Expire(ctx context.Context, paymentID string) (domain.Payment, error) {
	pre, err := o.store.GetPayment(ctx, paymentID)
	if err != nil {
		return domain.Payment{}, err
	}
	var out domain.Payment
	err = o.store.InTx(ctx, func(tx Tx) error {
		rows, err := tx.LockCheckoutPayments(ctx, pre.CheckoutID)
		if err != nil {
			return err
		}
		cur, ok := findPayment(rows, paymentID)
		if !ok {
			return ErrNotFound
		}
		out = cur
		if cur.Status != domain.StatusPending {
			return nil
		}
		cur.Status, cur.UpdatedAt = domain.StatusExpired, o.now()
		if err := tx.UpdatePayment(ctx, cur); err != nil {
			return err
		}
		out = cur
		return nil
	})
	if err != nil {
		return domain.Payment{}, fmt.Errorf("expire payment: %w", err)
	}
	return out, nil
}
