-- Idempotent-consumer marker (pkg/inbox). Services copy this into one of
-- their migrations and call MarkProcessed in the same transaction as the
-- event's effect.
CREATE TABLE IF NOT EXISTS processed_events (
    consumer     text        NOT NULL,
    event_id     uuid        NOT NULL,
    processed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, event_id)
);

CREATE INDEX IF NOT EXISTS processed_events_processed_at_idx ON processed_events (processed_at);
