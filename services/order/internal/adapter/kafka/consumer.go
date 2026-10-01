// Package kafka consumes payment.events: a captured payment marks its orders
// paid, a refund marks its order refunded.
package kafka

import (
	"context"
	"log/slog"
	"slices"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/pkg/kafkax"
)

// GroupID is order-svc's consumer group (WS3' §5).
const GroupID = "order-svc"

// PaymentEvents applies the payment events order-svc cares about.
type PaymentEvents interface {
	PaymentSucceeded(ctx context.Context, eventID string, ev event.PaymentSucceeded) error
	PaymentRefunded(ctx context.Context, eventID string, ev event.PaymentRefunded) error
}

// Handler turns payment events into order changes.
type Handler struct {
	events PaymentEvents
	logger *slog.Logger
}

// NewHandler returns a Handler.
func NewHandler(p PaymentEvents, logger *slog.Logger) *Handler {
	return &Handler{events: p, logger: logger}
}

// Handle processes one record. Records that can never succeed (undecodable,
// unsupported, malformed) are logged and skipped so they don't block the
// partition; use-case errors are returned so kafkax retries with backoff.
func (h *Handler) Handle(ctx context.Context, m kafkax.Message) error {
	env, err := event.Decode(m.Value)
	if err != nil {
		h.logger.WarnContext(ctx, "skipping undecodable payment event", "error", err)
		return nil
	}
	switch env.EventType {
	case event.TypePaymentSucceeded:
		if !h.usable(ctx, env) {
			return nil
		}
		data, err := event.DecodeData[event.PaymentSucceeded](env)
		if err != nil {
			h.logger.WarnContext(ctx, "skipping malformed PaymentSucceeded", "event_id", env.EventID, "error", err)
			return nil
		}
		if _, err := uuid.Parse(data.CheckoutID); err != nil {
			h.logger.WarnContext(ctx, "skipping PaymentSucceeded with invalid checkout_id",
				"event_id", env.EventID, "checkout_id", data.CheckoutID)
			return nil
		}
		// One unusable allocation must not cost the orders beside it their
		// payment, so drop it and apply the rest.
		kept := slices.DeleteFunc(slices.Clone(data.Allocations), func(a event.Allocation) bool {
			if _, err := uuid.Parse(a.OrderID); err != nil {
				h.logger.WarnContext(ctx, "dropping allocation with invalid order_id",
					"event_id", env.EventID, "order_id", a.OrderID, "payment_id", data.PaymentID)
				return true
			}
			return false
		})
		data.Allocations = kept
		return h.events.PaymentSucceeded(ctx, env.EventID, data)
	case event.TypePaymentRefunded:
		if !h.usable(ctx, env) {
			return nil
		}
		data, err := event.DecodeData[event.PaymentRefunded](env)
		if err != nil {
			h.logger.WarnContext(ctx, "skipping malformed PaymentRefunded", "event_id", env.EventID, "error", err)
			return nil
		}
		if data.OrderID != nil {
			if _, err := uuid.Parse(*data.OrderID); err != nil {
				h.logger.WarnContext(ctx, "skipping PaymentRefunded with invalid order_id",
					"event_id", env.EventID, "order_id", *data.OrderID)
				return nil
			}
		}
		return h.events.PaymentRefunded(ctx, env.EventID, data)
	default:
		return nil
	}
}

// usable reports whether env's event ID can key the inbox. A record whose ID is
// not a UUID would be recorded under a different key on every delivery, so it
// can never be recognised as a redelivery.
func (h *Handler) usable(ctx context.Context, env event.Envelope) bool {
	if _, err := uuid.Parse(env.EventID); err != nil {
		h.logger.WarnContext(ctx, "skipping payment event with invalid event_id", "event_id", env.EventID)
		return false
	}
	return true
}

// NewConsumer joins GroupID on event.TopicPaymentEvents with h.
func NewConsumer(brokers []string, h *Handler, logger *slog.Logger) (*kafkax.Consumer, error) {
	return kafkax.NewConsumer(kafkax.ConsumerConfig{
		Brokers: brokers,
		Group:   GroupID,
		Topics:  []string{event.TopicPaymentEvents},
		Logger:  logger,
	}, h.Handle)
}
