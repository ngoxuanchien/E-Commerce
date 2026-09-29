-- name: InsertPayment :exec
INSERT INTO payments (id, checkout_id, buyer_id, method, provider, status, amount, captured,
                      provider_ref, provider_txn_id, redirect_url, expires_at, failure_reason,
                      version, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16);

-- name: InsertAllocation :exec
INSERT INTO payment_allocations (payment_id, order_id, seller_user_id, amount, commission_amount)
VALUES ($1, $2, $3, $4, $5);

-- name: ListAllocations :many
SELECT * FROM payment_allocations WHERE payment_id = $1 ORDER BY order_id;

-- name: GetPayment :one
SELECT * FROM payments WHERE id = $1;

-- name: GetPaymentByProviderRef :one
SELECT * FROM payments WHERE provider = $1 AND provider_ref = $2;

-- name: ListCheckoutPayments :many
SELECT * FROM payments WHERE checkout_id = $1 ORDER BY id;

-- name: LockCheckoutPayments :many
SELECT * FROM payments WHERE checkout_id = $1 ORDER BY id FOR UPDATE;

-- name: ListPendingOlderThan :many
SELECT * FROM payments WHERE status = 'PENDING' AND created_at < @before ORDER BY created_at, id LIMIT @page_limit;

-- name: UpdatePayment :execrows
UPDATE payments
SET status = @status, captured = @captured, provider_txn_id = @provider_txn_id,
    failure_reason = @failure_reason, version = version + 1, updated_at = @updated_at
WHERE id = @id AND version = @version;

-- name: SetSession :execrows
UPDATE payments SET provider_ref = @provider_ref, redirect_url = @redirect_url WHERE id = @id;

-- name: CapturedAllocationFor :one
SELECT p.id, p.checkout_id, p.buyer_id, p.method, p.provider, p.status, p.amount, p.captured,
       p.provider_ref, p.provider_txn_id, p.redirect_url, p.expires_at, p.failure_reason,
       p.version, p.created_at, p.updated_at,
       a.order_id AS alloc_order_id, a.seller_user_id AS alloc_seller_user_id,
       a.amount AS alloc_amount, a.commission_amount AS alloc_commission_amount
FROM payment_allocations a
JOIN payments p ON p.id = a.payment_id
WHERE a.order_id = $1 AND p.captured;
