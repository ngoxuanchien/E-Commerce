-- name: EnsureAccount :exec
INSERT INTO accounts (id, code, type, owner_user_id, order_id)
VALUES (@id, @code, @type, sqlc.narg(owner_user_id), sqlc.narg(order_id))
ON CONFLICT (code) DO NOTHING;

-- name: AccountIDsByCode :many
SELECT id, code FROM accounts WHERE code = ANY(@codes::text[]);

-- name: LockAccounts :many
SELECT id FROM accounts WHERE id = ANY(@ids::uuid[]) ORDER BY id FOR UPDATE;

-- name: AccountBalance :one
SELECT coalesce(sum(amount), 0)::bigint AS balance FROM entries WHERE account_id = @account_id;

-- name: TransactionIDByKey :one
SELECT id FROM journal_transactions WHERE idempotency_key = @idempotency_key;

-- name: InsertTransaction :execrows
INSERT INTO journal_transactions (id, idempotency_key, kind, refs, source_event_id, occurred_at)
VALUES (@id, @idempotency_key, @kind, @refs, sqlc.narg(source_event_id), @occurred_at)
ON CONFLICT (idempotency_key) DO NOTHING;

-- name: InsertEntry :exec
INSERT INTO entries (id, transaction_id, account_id, amount)
VALUES (@id, @transaction_id, @account_id, @amount);
