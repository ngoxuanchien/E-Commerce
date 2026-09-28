-- Transactional outbox table (pkg/outbox). Services that publish events copy
-- this into one of their own migrations.
CREATE TABLE IF NOT EXISTS outbox (
    id           uuid        PRIMARY KEY,
    topic        text        NOT NULL,
    msg_key      text        NOT NULL,
    payload      jsonb       NOT NULL,
    headers      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz NULL
);

CREATE INDEX IF NOT EXISTS outbox_unpublished_idx
    ON outbox (created_at, id)
    WHERE published_at IS NULL;
