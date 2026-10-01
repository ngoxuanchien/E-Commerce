package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/domain"
)

// ConsumerName is ledger-svc's consumer group and processed_events consumer.
const ConsumerName = "ledger-svc"

var (
	// ErrInvalidEvent: the event can never be applied (bad ids, undecodable data).
	ErrInvalidEvent = errors.New("ledger: invalid event")
	// ErrAllocationMissing: a refund arrived before its capture. Retryable.
	ErrAllocationMissing = errors.New("ledger: no escrow allocation for the order yet")
)

// EventHandler applies payment.events and order.events to the ledger (WS2' §6).
type EventHandler struct {
	store  Store
	logger *slog.Logger
	now    func() time.Time
}

func NewEventHandler(store Store, logger *slog.Logger, now func() time.Time) *EventHandler {
	return &EventHandler{store: store, logger: logger, now: now}
}

// source is the event a posting comes from.
type source struct {
	eventID    uuid.UUID
	occurredAt time.Time
}

func (s source) stamp(j domain.Journal) domain.Journal {
	j.SourceEventID = &s.eventID
	j.OccurredAt = s.occurredAt
	return j
}

type applyFunc func(ctx context.Context, tx Tx, src source) error

// Handle applies one event in one database transaction: processed_events,
// then the postings. It returns nil when the event is done, a duplicate, not
// a ledger event, or can never be applied (logged and skipped, so it cannot
// block its partition). Other errors are returned so the consumer retries.
func (h *EventHandler) Handle(ctx context.Context, env event.Envelope) error {
	var apply applyFunc
	var err error
	switch env.EventType {
	case event.TypePaymentSucceeded:
		apply, err = bind(env, h.paymentSucceeded)
	case event.TypeOrderCompleted:
		apply, err = bind(env, func(ctx context.Context, tx Tx, src source, d event.OrderCompleted) error {
			return h.orderCompleted(ctx, tx, src, env, d)
		})
	case event.TypePaymentRefunded:
		apply, err = bind(env, h.paymentRefunded)
	default:
		return nil
	}
	eventID, idErr := uuid.Parse(env.EventID)
	if err == nil && idErr != nil {
		err = fmt.Errorf("%w: event_id %q", ErrInvalidEvent, env.EventID)
	}
	if err != nil {
		h.skip(ctx, env, err)
		return nil
	}
	src := source{eventID: eventID, occurredAt: env.OccurredAt.UTC()}
	err = h.store.InTx(ctx, func(tx Tx) error {
		first, err := tx.MarkProcessed(ctx, eventID)
		if err != nil || !first {
			return err
		}
		return apply(ctx, tx, src)
	})
	if err != nil && permanent(err) {
		h.skip(ctx, env, err)
		return nil
	}
	return err
}

func bind[T event.Payload](env event.Envelope, fn func(context.Context, Tx, source, T) error) (applyFunc, error) {
	d, err := event.DecodeData[T](env)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidEvent, err)
	}
	return func(ctx context.Context, tx Tx, src source) error { return fn(ctx, tx, src, d) }, nil
}

// permanent reports errors that no retry can fix.
func permanent(err error) bool {
	return errors.Is(err, ErrInvalidEvent) || errors.Is(err, ErrWouldOverdraw) ||
		errors.Is(err, domain.ErrInvalidPosting) || errors.Is(err, domain.ErrTooFewLines) ||
		errors.Is(err, domain.ErrZeroLine) || errors.Is(err, domain.ErrUnbalanced)
}

func (h *EventHandler) skip(ctx context.Context, env event.Envelope, err error) {
	h.logger.ErrorContext(ctx, "ledger skipped an event it can never apply",
		"alert", errors.Is(err, ErrWouldOverdraw), "event_id", env.EventID, "event_type", env.EventType, "error", err)
}

func parseID(field, s string) (uuid.UUID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: %s %q", ErrInvalidEvent, field, s)
	}
	return u, nil
}

func (h *EventHandler) paymentSucceeded(ctx context.Context, tx Tx, src source, d event.PaymentSucceeded) error {
	paymentID, err := parseID("payment_id", d.PaymentID)
	if err != nil {
		return err
	}
	checkoutID, err := parseID("checkout_id", d.CheckoutID)
	if err != nil {
		return err
	}
	pay, err := domain.Pay(paymentID, checkoutID, d.Provider, d.Amount)
	if err != nil {
		return err
	}
	if _, err := Post(ctx, tx, src.stamp(pay)); err != nil {
		return err
	}
	if len(d.Allocations) == 0 {
		return nil // a duplicate charge: it waits in suspense for its refund
	}
	allocs := make([]domain.Allocation, 0, len(d.Allocations))
	for _, a := range d.Allocations {
		orderID, err := parseID("allocations.order_id", a.OrderID)
		if err != nil {
			return err
		}
		sellerID, err := parseID("allocations.seller_user_id", a.SellerUserID)
		if err != nil {
			return err
		}
		allocs = append(allocs, domain.Allocation{OrderID: orderID, PaymentID: paymentID, CheckoutID: checkoutID,
			SellerUserID: sellerID, Amount: a.Amount, Commission: a.CommissionAmount, Provider: d.Provider})
	}
	capture, err := domain.Capture(paymentID, checkoutID, d.Amount, allocs)
	if err != nil {
		return err
	}
	// Without the lock, two payments on different partitions could both pass
	// the guard below and CAPTURE one order twice. After the wait the guard's
	// statements see the winner's committed allocation (READ COMMITTED).
	orderIDs := make([]uuid.UUID, len(allocs))
	for i, a := range allocs {
		orderIDs[i] = a.OrderID
	}
	if err := tx.LockOrders(ctx, orderIDs); err != nil {
		return err
	}
	for _, a := range allocs {
		existing, found, err := tx.GetAllocation(ctx, a.OrderID)
		if err != nil {
			return err
		}
		if found && existing.PaymentID != paymentID {
			h.logger.ErrorContext(ctx, "order already captured by another payment; this payment stays in suspense",
				"alert", true, "event_id", src.eventID, "order_id", a.OrderID, "payment_id", paymentID, "captured_by", existing.PaymentID)
			return nil
		}
	}
	for _, a := range allocs {
		if err := tx.InsertAllocation(ctx, a); err != nil {
			return err
		}
	}
	if _, err := Post(ctx, tx, src.stamp(capture)); err != nil {
		return err
	}
	return h.applyPending(ctx, tx, allocs)
}

// applyPending releases the orders whose OrderCompleted arrived before this capture.
func (h *EventHandler) applyPending(ctx context.Context, tx Tx, allocs []domain.Allocation) error {
	byOrder := make(map[uuid.UUID]domain.Allocation, len(allocs))
	ids := make([]uuid.UUID, 0, len(allocs))
	for _, a := range allocs {
		byOrder[a.OrderID] = a
		ids = append(ids, a.OrderID)
	}
	rows, err := tx.TakePending(ctx, ids)
	if err != nil {
		return err
	}
	for _, r := range rows {
		src, d, err := decodePending(r)
		if err != nil {
			h.logger.ErrorContext(ctx, "dropping an undecodable pending posting", "order_id", r.OrderID, "error", err)
		} else if err := h.release(ctx, tx, src, byOrder[r.OrderID], d); err != nil {
			return err
		}
		if err := tx.DeletePending(ctx, r.OrderID); err != nil {
			return err
		}
	}
	return nil
}

func decodePending(r PendingPosting) (source, event.OrderCompleted, error) {
	env, err := event.Decode(r.Envelope)
	if err != nil {
		return source{}, event.OrderCompleted{}, err
	}
	d, err := event.DecodeData[event.OrderCompleted](env)
	if err != nil {
		return source{}, event.OrderCompleted{}, err
	}
	id, err := parseID("event_id", env.EventID)
	if err != nil {
		return source{}, event.OrderCompleted{}, err
	}
	return source{eventID: id, occurredAt: env.OccurredAt.UTC()}, d, nil
}

func (h *EventHandler) orderCompleted(ctx context.Context, tx Tx, src source, env event.Envelope, d event.OrderCompleted) error {
	if d.PaymentMethod == "" || d.PaymentMethod == event.PaymentMethodCOD {
		return nil // COD never enters the ledger
	}
	orderID, err := parseID("order_id", d.OrderID)
	if err != nil {
		return err
	}
	a, found, err := tx.GetAllocation(ctx, orderID)
	if err != nil {
		return err
	}
	if !found { // PaymentSucceeded travels on another topic and hasn't arrived yet
		raw, err := json.Marshal(env)
		if err != nil {
			return err
		}
		return tx.InsertPending(ctx, orderID, raw, h.now())
	}
	return h.release(ctx, tx, src, a, d)
}

// release posts RELEASE from the allocation, which is what was captured.
func (h *EventHandler) release(ctx context.Context, tx Tx, src source, a domain.Allocation, d event.OrderCompleted) error {
	if d.Total != a.Amount || d.CommissionAmount != a.Commission {
		h.logger.ErrorContext(ctx, "OrderCompleted amounts differ from the captured allocation; releasing the allocation",
			"order_id", a.OrderID, "event_total", d.Total, "event_commission", d.CommissionAmount,
			"allocation_amount", a.Amount, "allocation_commission", a.Commission)
	}
	j, err := domain.Release(a)
	if err != nil {
		return err
	}
	_, err = Post(ctx, tx, src.stamp(j))
	return err
}

func (h *EventHandler) paymentRefunded(ctx context.Context, tx Tx, src source, d event.PaymentRefunded) error {
	refundID, err := parseID("refund_id", d.RefundID)
	if err != nil {
		return err
	}
	paymentID, err := parseID("payment_id", d.PaymentID)
	if err != nil {
		return err
	}
	checkoutID, err := parseID("checkout_id", d.CheckoutID)
	if err != nil {
		return err
	}
	if d.OrderID == nil {
		j, err := domain.DuplicateRefund(refundID, paymentID, checkoutID, d.Provider, d.Amount)
		if err != nil {
			return err
		}
		_, err = Post(ctx, tx, src.stamp(j))
		return err
	}
	orderID, err := parseID("order_id", *d.OrderID)
	if err != nil {
		return err
	}
	a, found, err := tx.GetAllocation(ctx, orderID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: order %s", ErrAllocationMissing, orderID)
	}
	if paymentID != a.PaymentID {
		h.logger.ErrorContext(ctx, "refund for another payment than the one that captured the order; skipping",
			"alert", true, "order_id", orderID, "refund_id", refundID, "payment_id", paymentID, "captured_by", a.PaymentID)
		return fmt.Errorf("%w: refund for another payment (%s) than the capture (%s)", ErrInvalidEvent, paymentID, a.PaymentID)
	}
	if d.Amount != a.Amount || d.Provider != a.Provider {
		h.logger.ErrorContext(ctx, "PaymentRefunded differs from the captured allocation; refunding the allocation",
			"order_id", orderID, "event_amount", d.Amount, "event_provider", d.Provider,
			"allocation_amount", a.Amount, "allocation_provider", a.Provider)
	}
	j, err := domain.Refund(refundID, a)
	if err != nil {
		return err
	}
	_, err = Post(ctx, tx, src.stamp(j))
	return err
}
