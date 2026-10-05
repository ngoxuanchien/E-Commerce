-- FAKE adapter only: makes Refund idempotent per refund id, so a retried call never adds to
-- fake_sessions.refunded twice.
CREATE TABLE fake_refunds (
    refund_id  text        PRIMARY KEY,
    payment_id uuid        NOT NULL REFERENCES payments (id),
    amount     bigint      NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
