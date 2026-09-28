// Package kafka consumes order.events: OrderCancelled returns reserved stock.
package kafka

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/pkg/kafkax"
)

// GroupID is catalog-svc's consumer group (WS2 §3).
const GroupID = "catalog-svc"

// Releaser releases an order's stock at most once per event.
type Releaser interface {
	ReleaseForEvent(ctx context.Context, eventID, orderID uuid.UUID) error
}

// Handler turns order events into stock releases.
type Handler struct {
	releaser Releaser
	logger   *slog.Logger
}

// NewHandler returns a Handler.
func NewHandler(r Releaser, logger *slog.Logger) *Handler {
	return &Handler{releaser: r, logger: logger}
}

// Handle processes one record. Records that can never succeed (undecodable,
// unsupported, malformed) are logged and skipped so they don't block the
// partition; release errors are returned so kafkax retries with backoff.
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
	eventID, err := uuid.Parse(env.EventID)
	if err != nil {
		h.logger.WarnContext(ctx, "skipping OrderCancelled with invalid event_id", "event_id", env.EventID)
		return nil
	}
	orderID, err := uuid.Parse(data.OrderID)
	if err != nil {
		h.logger.WarnContext(ctx, "skipping OrderCancelled with invalid order_id", "event_id", env.EventID, "order_id", data.OrderID)
		return nil
	}
	if err := h.releaser.ReleaseForEvent(ctx, eventID, orderID); err != nil {
		return err
	}
	h.logger.InfoContext(ctx, "released stock for cancelled order", "event_id", eventID, "order_id", orderID)
	return nil
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
