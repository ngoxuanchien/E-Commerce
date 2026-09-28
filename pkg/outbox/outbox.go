// Package outbox implements the transactional outbox: Write inserts a message
// in the caller's transaction; Relay publishes committed messages to Kafka.
package outbox

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// SchemaSQL creates the outbox table and index.
//
//go:embed schema.sql
var SchemaSQL string

// Message is an event waiting to be published.
type Message struct {
	Topic   string
	Key     string
	Payload []byte // JSON
	Headers map[string]string
}

// Execer is satisfied by pgx.Tx (and pgxpool.Pool, though Write is meant to
// run inside the business transaction).
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Write stores m in the outbox using tx. The trace context of ctx is saved in
// the headers so the published message continues the originating trace.
func Write(ctx context.Context, tx Execer, m Message) error {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("outbox id: %w", err)
	}
	h := maps.Clone(m.Headers)
	if h == nil {
		h = map[string]string{}
	}
	otel.GetTextMapPropagator().Inject(ctx, propagation.MapCarrier(h))
	headers, err := json.Marshal(h)
	if err != nil {
		return fmt.Errorf("outbox headers: %w", err)
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO outbox (id, topic, msg_key, payload, headers) VALUES ($1, $2, $3, $4, $5)`,
		id, m.Topic, m.Key, string(m.Payload), string(headers))
	if err != nil {
		return fmt.Errorf("outbox insert: %w", err)
	}
	return nil
}
