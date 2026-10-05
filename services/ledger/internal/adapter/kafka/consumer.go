// Package kafka consumes payment.events and order.events into the ledger.
package kafka

import (
	"context"
	"log/slog"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/pkg/kafkax"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/app"
)

// GroupID is ledger-svc's consumer group (WS0' §5).
const GroupID = app.ConsumerName

// Handler decodes records and hands them to the event handler.
type Handler struct {
	events *app.EventHandler
	logger *slog.Logger
}

func NewHandler(events *app.EventHandler, logger *slog.Logger) *Handler {
	return &Handler{events: events, logger: logger}
}

// Handle processes one record. Undecodable records are logged and skipped so
// they don't block the partition; the event handler decides the rest.
func (h *Handler) Handle(ctx context.Context, m kafkax.Message) error {
	env, err := event.Decode(m.Value)
	if err != nil {
		h.logger.WarnContext(ctx, "skipping undecodable event", "topic", m.Topic, "error", err)
		return nil
	}
	return h.events.Handle(ctx, env)
}

// NewConsumer joins GroupID on payment.events and order.events.
func NewConsumer(brokers []string, h *Handler, logger *slog.Logger) (*kafkax.Consumer, error) {
	return kafkax.NewConsumer(kafkax.ConsumerConfig{
		Brokers: brokers,
		Group:   GroupID,
		Topics:  []string{event.TopicPaymentEvents, event.TopicOrderEvents},
		Logger:  logger,
	}, h.Handle)
}
