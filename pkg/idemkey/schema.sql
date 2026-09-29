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
