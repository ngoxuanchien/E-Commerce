-- ledger-svc schema (WS2' §5) and the invariants the database enforces (§4).

CREATE TABLE accounts (
    id            uuid        PRIMARY KEY,
    code          text        NOT NULL UNIQUE,
    type          text        NOT NULL CHECK (type IN ('asset', 'liability', 'revenue')),
    owner_user_id uuid        NULL,
    order_id      uuid        NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX accounts_escrow_owner_idx ON accounts (owner_user_id) WHERE code LIKE 'escrow:%';

CREATE TABLE journal_transactions (
    id              uuid        PRIMARY KEY,
    idempotency_key text        NOT NULL UNIQUE,
    kind            text        NOT NULL CHECK (kind IN ('PAY', 'CAPTURE', 'RELEASE', 'REFUND', 'DUPLICATE_REFUND',
                                                         'PAYOUT_REQUEST', 'PAYOUT_SETTLE', 'PAYOUT_FAIL')),
    refs            jsonb       NOT NULL DEFAULT '{}'::jsonb,
    source_event_id uuid        NULL,
    occurred_at     timestamptz NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE entries (
    id             uuid        PRIMARY KEY,
    transaction_id uuid        NOT NULL REFERENCES journal_transactions (id),
    account_id     uuid        NOT NULL REFERENCES accounts (id),
    amount         bigint      NOT NULL CHECK (amount <> 0),
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX entries_account_created_idx ON entries (account_id, created_at DESC, id DESC);
CREATE INDEX entries_transaction_idx ON entries (transaction_id);

CREATE TABLE escrow_allocations (
    order_id          uuid        PRIMARY KEY,
    payment_id        uuid        NOT NULL,
    checkout_id       uuid        NOT NULL,
    seller_user_id    uuid        NOT NULL,
    amount            bigint      NOT NULL CHECK (amount > 0),
    commission_amount bigint      NOT NULL CHECK (commission_amount >= 0 AND commission_amount < amount),
    provider          text        NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE payouts (
    id             uuid        PRIMARY KEY,
    seller_user_id uuid        NOT NULL,
    amount         bigint      NOT NULL CHECK (amount > 0),
    status         text        NOT NULL CHECK (status IN ('REQUESTED', 'SETTLED', 'FAILED')),
    failure_reason text        NULL,
    requested_at   timestamptz NOT NULL,
    settled_at     timestamptz NULL,
    version        int         NOT NULL DEFAULT 1
);
CREATE INDEX payouts_seller_requested_idx ON payouts (seller_user_id, requested_at DESC, id DESC);
CREATE INDEX payouts_status_requested_idx ON payouts (status, requested_at);

-- OrderCompleted events that arrived before their capture (WS2' §6).
CREATE TABLE pending_postings (
    order_id        uuid        PRIMARY KEY,
    event           jsonb       NOT NULL,
    attempts        int         NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL,
    last_attempt_at timestamptz NULL
);

-- Invariant 2: append-only.
CREATE FUNCTION ledger_reject_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'ledger: % on % is not allowed (append-only)', TG_OP, TG_TABLE_NAME
        USING ERRCODE = 'restrict_violation';
END
$$;

CREATE TRIGGER journal_transactions_append_only BEFORE UPDATE OR DELETE ON journal_transactions
    FOR EACH ROW EXECUTE FUNCTION ledger_reject_mutation();
CREATE TRIGGER journal_transactions_no_truncate BEFORE TRUNCATE ON journal_transactions
    FOR EACH STATEMENT EXECUTE FUNCTION ledger_reject_mutation();
CREATE TRIGGER entries_append_only BEFORE UPDATE OR DELETE ON entries
    FOR EACH ROW EXECUTE FUNCTION ledger_reject_mutation();
CREATE TRIGGER entries_no_truncate BEFORE TRUNCATE ON entries
    FOR EACH STATEMENT EXECUTE FUNCTION ledger_reject_mutation();
CREATE TRIGGER escrow_allocations_append_only BEFORE UPDATE OR DELETE ON escrow_allocations
    FOR EACH ROW EXECUTE FUNCTION ledger_reject_mutation();
CREATE TRIGGER escrow_allocations_no_truncate BEFORE TRUNCATE ON escrow_allocations
    FOR EACH STATEMENT EXECUTE FUNCTION ledger_reject_mutation();

-- Invariant 1: every transaction has at least 2 entries summing to 0, checked at commit.
CREATE FUNCTION ledger_check_transaction_balanced() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    tx_id uuid;
    n     bigint;
    total numeric;
BEGIN
    IF TG_TABLE_NAME = 'entries' THEN
        tx_id := NEW.transaction_id;
    ELSE
        tx_id := NEW.id;
    END IF;
    SELECT count(*), coalesce(sum(amount), 0) INTO n, total FROM entries WHERE transaction_id = tx_id;
    IF n < 2 THEN
        RAISE EXCEPTION 'ledger: transaction % has % entries (at least 2 required)', tx_id, n
            USING ERRCODE = 'check_violation';
    END IF;
    IF total <> 0 THEN
        RAISE EXCEPTION 'ledger: transaction % is unbalanced (sum %)', tx_id, total
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL;
END
$$;

CREATE CONSTRAINT TRIGGER entries_balanced AFTER INSERT ON entries
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION ledger_check_transaction_balanced();
CREATE CONSTRAINT TRIGGER journal_transactions_have_entries AFTER INSERT ON journal_transactions
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION ledger_check_transaction_balanced();

-- Invariant 3 backstop: no liability account ends a transaction in debit.
-- The posting engine checks first under FOR UPDATE (that lock is what makes
-- concurrent debits correct); this catches any other writer.
CREATE FUNCTION ledger_check_liability_not_in_debit() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    acc_type text;
    balance  numeric;
BEGIN
    SELECT type INTO acc_type FROM accounts WHERE id = NEW.account_id;
    IF acc_type = 'liability' THEN
        SELECT coalesce(sum(amount), 0) INTO balance FROM entries WHERE account_id = NEW.account_id;
        IF balance > 0 THEN
            RAISE EXCEPTION 'ledger: liability account % would be in debit (%)', NEW.account_id, balance
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;
    RETURN NULL;
END
$$;

CREATE CONSTRAINT TRIGGER entries_liability_not_in_debit AFTER INSERT ON entries
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION ledger_check_liability_not_in_debit();

-- pkg/inbox SchemaSQL (verbatim)
CREATE TABLE IF NOT EXISTS processed_events (
    consumer     text        NOT NULL,
    event_id     uuid        NOT NULL,
    processed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, event_id)
);

-- pkg/idemkey SchemaSQL (verbatim)
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

-- pkg/outbox SchemaSQL (verbatim)
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
