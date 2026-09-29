-- payment-svc schema (spec §2). The shared pkg/inbox, pkg/idemkey and
-- pkg/outbox blocks at the end are copied verbatim from pkg/*/schema.sql.
CREATE TABLE payments (
    id              uuid        PRIMARY KEY,
    checkout_id     uuid        NOT NULL,
    buyer_id        uuid        NOT NULL,
    method          text        NOT NULL CHECK (method IN ('VNPAY', 'STRIPE')),
    provider        text        NOT NULL CHECK (provider IN ('vnpay', 'stripe', 'fake')),
    status          text        NOT NULL CHECK (status IN ('PENDING', 'SUCCEEDED', 'FAILED', 'EXPIRED', 'DUPLICATE')),
    amount          bigint      NOT NULL CHECK (amount > 0),
    captured        boolean     NOT NULL DEFAULT false,
    provider_ref    text        NULL,
    provider_txn_id text        NULL,
    redirect_url    text        NULL,
    expires_at      timestamptz NOT NULL,
    failure_reason  text        NULL,
    version         int         NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL,
    CONSTRAINT payments_provider_ref_key UNIQUE (provider, provider_ref)
);

CREATE UNIQUE INDEX payments_one_open ON payments (checkout_id) WHERE status = 'PENDING';
CREATE UNIQUE INDEX payments_one_captured ON payments (checkout_id) WHERE captured;
CREATE INDEX payments_status_created_idx ON payments (status, created_at);
CREATE INDEX payments_checkout_idx ON payments (checkout_id);

CREATE TABLE payment_allocations (
    payment_id        uuid   NOT NULL REFERENCES payments (id),
    order_id          uuid   NOT NULL,
    seller_user_id    uuid   NOT NULL,
    amount            bigint NOT NULL CHECK (amount >= 0),
    commission_amount bigint NOT NULL CHECK (commission_amount >= 0),
    PRIMARY KEY (payment_id, order_id)
);

CREATE INDEX payment_allocations_order_idx ON payment_allocations (order_id);

CREATE TABLE order_cancellations (
    order_id     uuid        PRIMARY KEY,
    checkout_id  uuid        NOT NULL,
    cancelled_at timestamptz NOT NULL
);

CREATE TABLE refunds (
    id                  uuid        PRIMARY KEY,
    payment_id          uuid        NOT NULL REFERENCES payments (id),
    order_id            uuid        NULL,
    amount              bigint      NOT NULL CHECK (amount > 0),
    status              text        NOT NULL CHECK (status IN ('PENDING', 'SUCCEEDED', 'FAILED')),
    provider_refund_ref text        NULL,
    attempts            int         NOT NULL DEFAULT 0,
    next_attempt_at     timestamptz NOT NULL,
    last_error          text        NULL,
    created_at          timestamptz NOT NULL,
    updated_at          timestamptz NOT NULL,
    CONSTRAINT refunds_payment_order_key UNIQUE (payment_id, order_id)
);

CREATE UNIQUE INDEX refunds_one_duplicate ON refunds (payment_id) WHERE order_id IS NULL;
CREATE INDEX refunds_status_next_idx ON refunds (status, next_attempt_at);

CREATE TABLE provider_events (
    provider    text        NOT NULL,
    event_key   text        NOT NULL,
    received_at timestamptz NOT NULL,
    PRIMARY KEY (provider, event_key)
);

-- FAKE adapter only (spec §2).
CREATE TABLE fake_sessions (
    payment_id uuid   PRIMARY KEY REFERENCES payments (id),
    outcome    text   NULL,
    hooks      jsonb  NOT NULL DEFAULT '[]'::jsonb,
    refunded   bigint NOT NULL DEFAULT 0
);

-- Idempotent-consumer marker (pkg/inbox). Services copy this into one of
-- their migrations and call MarkProcessed in the same transaction as the
-- event's effect.
CREATE TABLE IF NOT EXISTS processed_events (
    consumer     text        NOT NULL,
    event_id     uuid        NOT NULL,
    processed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, event_id)
);

-- Idempotency-Key store (pkg/idemkey). created_at is the claim: completing
-- or releasing a key requires it (see idemkey.go). Copy into a migration.
CREATE TABLE IF NOT EXISTS idempotency_keys (
    scope        text        NOT NULL,
    user_id      uuid        NOT NULL,
    key          text        NOT NULL,
    request_hash bytea       NOT NULL,
    response     jsonb       NULL,
    status_code  int         NULL,
    created_at   timestamptz NOT NULL,
    PRIMARY KEY (scope, user_id, key)
);

CREATE INDEX IF NOT EXISTS idempotency_keys_created_idx ON idempotency_keys (created_at);

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
