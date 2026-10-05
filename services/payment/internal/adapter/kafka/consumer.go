// Package kafka consumes order.events: OrderCancelled feeds the refund-due rule.
package kafka

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/pkg/kafkax"
)

// GroupID is payment-svc's consumer group.
const GroupID = "payment-svc"

// Canceller applies one OrderCancelled event at most once per event id.
type Canceller interface {
	OrderCancelled(ctx context.Context, eventID string, e event.OrderCancelled, at time.Time) error
}

// Handler turns order events into refund-due checks.
type Handler struct {
	c      Canceller
	logger *slog.Logger
}

func NewHandler(c Canceller, logger *slog.Logger) *Handler {
	return &Handler{c: c, logger: logger}
}

// Handle processes one record. Records that can never succeed (undecodable, unsupported, malformed)
// are logged and skipped so they don't block the partition; store errors are returned so kafkax
// retries with backoff.
func (h *Handler) Handle(ctx context.Context, m kafkax.Message) error {
	env, err := event.Decode(m.Value)
	if err != nil {
		h.logger.WarnContext(ctx, "skipping undecodable order event", "error", err)
		return nil
	}
	if env.EventType != event.TypeOrderCancelled {
		return nil
	}
	data, err := event.DecodeData[event.OrderCancelled](env)
	if err != nil {
		h.logger.WarnContext(ctx, "skipping malformed OrderCancelled", "event_id", env.EventID, "error", err)
		return nil
	}
	if _, err := uuid.Parse(env.EventID); err != nil {
		h.logger.WarnContext(ctx, "skipping OrderCancelled with invalid event_id", "event_id", env.EventID)
		return nil
	}
	if _, err := uuid.Parse(data.OrderID); err != nil {
		h.logger.WarnContext(ctx, "skipping OrderCancelled with invalid order_id", "event_id", env.EventID, "order_id", data.OrderID)
		return nil
	}
	if data.CheckoutID != "" {
		if _, err := uuid.Parse(data.CheckoutID); err != nil {
			h.logger.WarnContext(ctx, "skipping OrderCancelled with invalid checkout_id", "event_id", env.EventID, "checkout_id", data.CheckoutID)
			return nil
		}
	}
	return h.c.OrderCancelled(ctx, env.EventID, data, env.OccurredAt)
}

// NewConsumer joins GroupID on event.TopicOrderEvents with h.
func NewConsumer(brokers []string, h *Handler, logger *slog.Logger) (*kafkax.Consumer, error) {
	return kafkax.NewConsumer(kafkax.ConsumerConfig{
		Brokers: brokers,
		Group:   GroupID,
		Topics:  []string{event.TopicOrderEvents},
		Logger:  logger,
	}, h.Handle)
}
