// Package inbox records which events a consumer has already applied, so a
// redelivered Kafka message is recognised and skipped (ADR-0003).
package inbox

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// SchemaSQL creates processed_events. Copy it verbatim into a migration.
//
//go:embed schema.sql
var SchemaSQL string

// Execer is satisfied by pgx.Tx, *pgxpool.Pool and *pgx.Conn.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// MarkProcessed records (consumer, eventID). firstTime is false when it was
// already recorded, in which case the caller must skip the event. Call it in
// the same transaction as the event's effect, so both commit or neither does.
func MarkProcessed(ctx context.Context, db Execer, consumer, eventID string) (firstTime bool, err error) {
	tag, err := db.Exec(ctx,
		`INSERT INTO processed_events (consumer, event_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		consumer, eventID)
	if err != nil {
		return false, fmt.Errorf("inbox: mark %s/%s: %w", consumer, eventID, err)
	}
	return tag.RowsAffected() == 1, nil
}

// PurgeBefore deletes markers processed before t and reports how many went. A redelivery older
// than the retention window would be processed again, so t must be far older than any broker
// retention or retry horizon.
func PurgeBefore(ctx context.Context, db Execer, t time.Time) (int64, error) {
	tag, err := db.Exec(ctx, `DELETE FROM processed_events WHERE processed_at < $1`, t)
	if err != nil {
		return 0, fmt.Errorf("inbox: purge before %s: %w", t, err)
	}
	return tag.RowsAffected(), nil
}
