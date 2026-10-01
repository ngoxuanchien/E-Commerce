package app

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

// paymentEventAttempts is how many times an event is applied before its error
// goes back to the consumer, which redelivers the message with backoff.
const paymentEventAttempts = 2

// PaymentEventHandler applies payment-svc's events to the orders they cover
// (WS3' §5.4): a capture marks its orders paid (or records a late capture on an
// order that was cancelled unpaid), a refund marks its order refunded.
//
// Each event is applied at most once: its ID is recorded in the inbox in the
// same transaction as the effects, so a redelivery is a no-op. A lost
// optimistic-lock race is retried against the fresh state, because that state
// may still accept the event — a capture that lost to a cancel becomes a late
// capture rather than an error.
type PaymentEventHandler struct {
	store  Store
	now    func() time.Time
	logger *slog.Logger
}

// NewPaymentEventHandler uses now as its clock and logger for warnings (nil =
// UTC wall clock, slog.Default()).
func NewPaymentEventHandler(store Store, now func() time.Time, logger *slog.Logger) *PaymentEventHandler {
	if now == nil {
		now = defaultNow
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &PaymentEventHandler{store: store, now: now, logger: logger}
}

// PaymentSucceeded moves every allocated order out of AWAITING_PAYMENT, or
// records the capture as a late one on an order that was cancelled unpaid.
// Allocations are applied in ascending order ID, the lock order of a buyer
// cancel fan-out and the sweeper, so they cannot deadlock. An allocation of an
// unknown order, or of an order of another checkout, is skipped with a
// warning; an event with no
// allocations (a duplicate charge, refunded automatically) changes nothing but
// is still marked processed.
func (h *PaymentEventHandler) PaymentSucceeded(ctx context.Context, eventID string, ev event.PaymentSucceeded) error {
	allocations := slices.Clone(ev.Allocations)
	slices.SortFunc(allocations, func(a, b event.Allocation) int { return strings.Compare(a.OrderID, b.OrderID) })
	for range paymentEventAttempts {
		err := h.apply(ctx, eventID, func(ctx context.Context, tx Tx) error {
			for _, a := range allocations {
				o, err := h.store.GetOrder(ctx, a.OrderID)
				if errors.Is(err, ErrNotFound) {
					h.logger.WarnContext(ctx, "skipping allocation of an unknown order",
						"event_id", eventID, "order_id", a.OrderID, "payment_id", ev.PaymentID)
					continue
				}
				if err != nil {
					return err
				}
				if o.CheckoutID != ev.CheckoutID {
					h.logger.WarnContext(ctx, "skipping allocation of an order of another checkout",
						"event_id", eventID, "order_id", a.OrderID, "order_checkout_id", o.CheckoutID,
						"checkout_id", ev.CheckoutID, "payment_id", ev.PaymentID)
					continue
				}
				if err := h.capture(ctx, tx, &o, ev.PaymentID); err != nil {
					return err
				}
			}
			return nil
		})
		if !lostRace(err) {
			return err
		}
	}
	return ErrConcurrentUpdate
}

// PaymentRefunded marks the refunded order REFUNDED. A nil OrderID — the refund
// of a duplicate charge — changes nothing.
func (h *PaymentEventHandler) PaymentRefunded(ctx context.Context, eventID string, ev event.PaymentRefunded) error {
	if ev.OrderID == nil {
		return h.apply(ctx, eventID, nil)
	}
	for range paymentEventAttempts {
		err := h.apply(ctx, eventID, func(ctx context.Context, tx Tx) error {
			o, err := h.store.GetOrder(ctx, *ev.OrderID)
			if errors.Is(err, ErrNotFound) {
				h.logger.WarnContext(ctx, "skipping refund of an unknown order",
					"event_id", eventID, "order_id", *ev.OrderID, "refund_id", ev.RefundID)
				return nil
			}
			if err != nil {
				return err
			}
			return h.refund(ctx, tx, &o)
		})
		if !lostRace(err) {
			return err
		}
	}
	return ErrConcurrentUpdate
}

// lostRace reports whether err is worth another attempt: only a lost optimistic
// lock is, because the order moved on while the event was being applied.
func lostRace(err error) bool { return errors.Is(err, ErrConcurrentUpdate) }

// apply records eventID in the inbox and runs effects in one transaction, so
// either both commit or neither does. effects may be nil (an event with nothing
// to apply). A redelivery is recorded but not applied.
func (h *PaymentEventHandler) apply(ctx context.Context, eventID string, effects func(context.Context, Tx) error) error {
	return h.store.InTx(ctx, func(tx Tx) error {
		firstTime, err := tx.MarkProcessed(ctx, eventID)
		if err != nil {
			return err
		}
		if !firstTime || effects == nil {
			return nil
		}
		return effects(ctx, tx)
	})
}

// capture applies a captured payment to one allocated order: the order waits
// for it, was cancelled before it (a late capture, only a money change), or has
// moved on without it.
func (h *PaymentEventHandler) capture(ctx context.Context, tx Tx, o *domain.Order, paymentID string) error {
	switch o.Status {
	case domain.StatusAwaitingPayment:
		return applyTransition(ctx, tx, o, domain.ActionMarkPaid, domain.SystemActor(), paymentID, h.now())
	case domain.StatusCancelled:
		if o.LateCapture(h.now()) {
			return tx.SavePaymentStatus(ctx, *o, o.Version)
		}
		return nil
	default:
		h.logger.InfoContext(ctx, "payment captured for an order that is not awaiting payment",
			"order_id", o.ID, "status", string(o.Status), "payment_id", paymentID)
		return nil
	}
}

// refund records a completed refund on o. The money change writes no history
// row and no event, because a refund never changes an order's status (WS3' §5).
func (h *PaymentEventHandler) refund(ctx context.Context, tx Tx, o *domain.Order) error {
	if !o.MarkRefunded(h.now()) {
		return nil
	}
	return tx.SavePaymentStatus(ctx, *o, o.Version)
}
