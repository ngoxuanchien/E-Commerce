package kafka_test

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ngoxuanchien/e-commerce/pkg/kafkax"
	kafkaadapter "github.com/ngoxuanchien/e-commerce/services/ledger/internal/adapter/kafka"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/app"
)

// Undecodable records must be skipped (nil), or kafkax would retry them forever.
func TestHandlerSkipsUndecodable(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	h := kafkaadapter.NewHandler(app.NewEventHandler(nil, logger, nil), logger)
	for _, v := range []string{`not json`, `{"event_id":"x","version":2}`, `{}`} {
		assert.NoError(t, h.Handle(context.Background(), kafkax.Message{Topic: "payment.events", Value: []byte(v)}), v)
	}
	assert.Contains(t, logs.String(), "skipping undecodable event")
}
