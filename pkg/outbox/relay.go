package outbox

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ngoxuanchien/e-commerce/pkg/kafkax"
	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
)

// Publisher sends messages; *kafkax.Producer implements it.
type Publisher interface {
	Publish(ctx context.Context, msgs ...kafkax.Message) error
}

// RelayConfig tunes the relay. Zero values use the defaults (100, 500ms).
type RelayConfig struct {
	BatchSize int
	Interval  time.Duration
	Logger    *slog.Logger
}

// Relay moves committed outbox rows to Kafka. Several replicas may run at
// once: rows are claimed with FOR UPDATE SKIP LOCKED. Delivery is
// at-least-once (a crash after publishing but before commit re-sends).
type Relay struct {
	pool *pgxpool.Pool
	pub  Publisher
	cfg  RelayConfig
}

func NewRelay(pool *pgxpool.Pool, pub Publisher, cfg RelayConfig) *Relay {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 100
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 500 * time.Millisecond
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Relay{pool: pool, pub: pub, cfg: cfg}
}

// Run processes batches until ctx is cancelled. A full batch is followed
// immediately by another; otherwise it waits Interval.
func (r *Relay) Run(ctx context.Context) error {
	for {
		n, err := r.ProcessBatch(ctx)
		if err != nil && ctx.Err() == nil {
			r.cfg.Logger.ErrorContext(ctx, "outbox relay batch failed", "error", err)
		}
		if n == r.cfg.BatchSize {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(r.cfg.Interval):
		}
	}
}

// ProcessBatch publishes up to BatchSize unpublished rows in creation order
// and marks them published, all in one transaction. It returns how many rows
// were published. On publish failure nothing is marked.
func (r *Relay) ProcessBatch(ctx context.Context) (int, error) {
	var published int
	err := pgxx.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, topic, msg_key, payload, headers
			FROM outbox
			WHERE published_at IS NULL
			ORDER BY created_at, id
			LIMIT $1
			FOR UPDATE SKIP LOCKED`, r.cfg.BatchSize)
		if err != nil {
			return fmt.Errorf("select outbox: %w", err)
		}
		var ids [][16]byte
		var msgs []kafkax.Message
		for rows.Next() {
			var (
				id      [16]byte
				m       kafkax.Message
				key     string
				payload []byte
			)
			if err := rows.Scan(&id, &m.Topic, &key, &payload, &m.Headers); err != nil {
				rows.Close()
				return fmt.Errorf("scan outbox: %w", err)
			}
			m.Key, m.Value = []byte(key), payload
			ids = append(ids, id)
			msgs = append(msgs, m)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate outbox: %w", err)
		}
		if len(msgs) == 0 {
			return nil
		}
		if err := r.pub.Publish(ctx, msgs...); err != nil {
			return fmt.Errorf("publish outbox: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE outbox SET published_at = now() WHERE id = ANY($1)`, ids); err != nil {
			return fmt.Errorf("mark outbox published: %w", err)
		}
		published = len(msgs)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return published, nil
}
