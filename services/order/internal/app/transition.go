package app

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/domain"
)

// applyTransition moves o through action by actor and persists it in tx:
// status (optimistic lock on o.Version), a history row and the outbox events.
// On success o reflects the stored state, including its new version.
func applyTransition(ctx context.Context, tx Tx, o *domain.Order, action domain.Action, actor domain.Actor, note string, at time.Time) error {
	ch, err := domain.Transition(*o, action, actor)
	if err != nil {
		return err
	}
	prev := o.Version
	o.Apply(ch, note, at)
	if err := tx.SaveTransition(ctx, *o, prev); err != nil {
		return err
	}
	o.Version = prev + 1
	for _, p := range transitionEvents(*o, ch) {
		if err := tx.Publish(ctx, o.ID, p, at); err != nil {
			return err
		}
	}
	return nil
}

// transitionEvents returns the events of a status change: always
// OrderStatusChanged, plus OrderCancelled for cancellations (catalog releases
// the order's stock when it consumes it) or OrderCompleted for completions —
// written for every completion, COD and online alike (WS3' §3).
func transitionEvents(o domain.Order, ch domain.StatusChange) []event.Payload {
	evs := []event.Payload{event.OrderStatusChanged{
		OrderID: o.ID, From: string(ch.From), To: string(ch.To), ActorID: ch.Actor.ID, ActorRole: string(ch.Actor.Role),
	}}
	switch ch.To {
	case domain.StatusCancelled:
		reason := ""
		if o.CancelReason != nil {
			reason = *o.CancelReason
		}
		evs = append(evs, event.OrderCancelled{
			OrderID: o.ID, Reason: reason, CancelledBy: string(ch.Actor.Role),
			CheckoutID: o.CheckoutID, PaymentMethod: o.PaymentMethod,
		})
	case domain.StatusCompleted:
		evs = append(evs, event.OrderCompleted{
			OrderID: o.ID, CheckoutID: o.CheckoutID, SellerUserID: o.SellerUserID,
			PaymentMethod: o.PaymentMethod, Total: o.Total, CommissionAmount: o.CommissionAmount,
			CompletedBy: string(ch.Actor.Role),
		})
	}
	return evs
}

func orderCreated(o domain.Order) event.OrderCreated {
	items := make([]event.OrderItem, len(o.Items))
	for i, it := range o.Items {
		items[i] = event.OrderItem{SkuID: it.SkuID, Quantity: it.Quantity, UnitPrice: it.UnitPrice}
	}
	return event.OrderCreated{
		OrderID: o.ID, CheckoutID: o.CheckoutID, BuyerID: o.BuyerID, ShopID: o.ShopID,
		SellerUserID: o.SellerUserID, Total: o.Total, Items: items,
		PaymentMethod: o.PaymentMethod, CommissionRateBps: o.CommissionRateBps, CommissionAmount: o.CommissionAmount,
	}
}

func newID() string { return uuid.Must(uuid.NewV7()).String() }

// defaultNow is the service clock: UTC, truncated to Postgres' precision.
func defaultNow() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }
