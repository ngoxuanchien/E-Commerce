-- name: InsertRefund :execrows
INSERT INTO refunds (id, payment_id, order_id, amount, status, provider_refund_ref, attempts,
                     next_attempt_at, last_error, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
ON CONFLICT DO NOTHING;

-- name: GetRefundByProviderRef :one
SELECT r.id, r.payment_id, r.order_id, r.amount, r.status, r.provider_refund_ref, r.attempts,
       r.next_attempt_at, r.last_error, p.checkout_id, p.provider
FROM refunds r JOIN payments p ON p.id = r.payment_id
WHERE p.provider = $1 AND r.provider_refund_ref = $2;

-- name: LockRefund :one
SELECT r.id, r.payment_id, r.order_id, r.amount, r.status, r.provider_refund_ref, r.attempts,
       r.next_attempt_at, r.last_error, p.checkout_id, p.provider
FROM refunds r JOIN payments p ON p.id = r.payment_id
WHERE r.id = $1
FOR UPDATE OF r;

-- name: ClaimDueRefunds :many
SELECT r.id, r.payment_id, r.order_id, r.amount, r.status, r.provider_refund_ref, r.attempts,
       r.next_attempt_at, r.last_error, p.checkout_id, p.provider
FROM refunds r JOIN payments p ON p.id = r.payment_id
WHERE r.status = 'PENDING' AND r.next_attempt_at <= @now
ORDER BY r.next_attempt_at, r.id
LIMIT @page_limit
FOR UPDATE OF r SKIP LOCKED;

-- name: UpdateRefund :execrows
UPDATE refunds
SET status = @status, provider_refund_ref = @provider_refund_ref, attempts = @attempts,
    next_attempt_at = @next_attempt_at, last_error = @last_error, updated_at = @updated_at
WHERE id = @id;
