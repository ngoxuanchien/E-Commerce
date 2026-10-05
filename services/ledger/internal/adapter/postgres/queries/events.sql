-- name: InsertAllocation :exec
INSERT INTO escrow_allocations (order_id, payment_id, checkout_id, seller_user_id, amount, commission_amount, provider)
VALUES (@order_id, @payment_id, @checkout_id, @seller_user_id, @amount, @commission_amount, @provider)
ON CONFLICT (order_id) DO NOTHING;

-- name: GetAllocation :one
SELECT * FROM escrow_allocations WHERE order_id = @order_id;

-- name: InsertPending :exec
INSERT INTO pending_postings (order_id, event, created_at)
VALUES (@order_id, @event, @created_at)
ON CONFLICT (order_id) DO NOTHING;

-- name: TakePending :many
SELECT * FROM pending_postings WHERE order_id = ANY(@order_ids::uuid[]) ORDER BY order_id FOR UPDATE;

-- name: ClaimPending :many
SELECT * FROM pending_postings ORDER BY last_attempt_at NULLS FIRST, created_at, order_id LIMIT @batch_size FOR UPDATE SKIP LOCKED;

-- name: DeletePending :exec
DELETE FROM pending_postings WHERE order_id = @order_id;

-- name: TouchPending :exec
UPDATE pending_postings SET attempts = attempts + 1, last_attempt_at = @at WHERE order_id = @order_id;

-- name: LockOrder :exec
SELECT pg_advisory_xact_lock(@key::bigint);
