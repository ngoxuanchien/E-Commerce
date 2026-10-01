-- name: InsertOrder :exec
INSERT INTO orders (id, checkout_id, buyer_id, shop_id, shop_name, seller_user_id, status,
                    subtotal, shipping_fee, total, shipping_address, payment_method,
                    cancel_reason, version, created_at, updated_at,
                    shop_slug, payment_status, payment_deadline, commission_rate_bps,
                    commission_amount, delivered_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16,
        $17, $18, $19, $20, $21, $22);

-- name: InsertOrderItem :exec
INSERT INTO order_items (id, order_id, sku_id, product_id, product_name, variant_name,
                         image_url, unit_price, quantity, line_total)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: InsertHistory :exec
INSERT INTO order_status_history (id, order_id, from_status, to_status, actor_id, actor_role, note, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: GetOrder :one
SELECT * FROM orders WHERE id = $1;

-- name: ListItemsByOrderIDs :many
SELECT * FROM order_items WHERE order_id = ANY(@order_ids::uuid[]) ORDER BY order_id, id;

-- name: ListHistoryByOrderIDs :many
SELECT * FROM order_status_history WHERE order_id = ANY(@order_ids::uuid[]) ORDER BY order_id, created_at, id;

-- name: ListBuyerOrders :many
SELECT * FROM orders
WHERE buyer_id = @buyer_id
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(before_created_at)::timestamptz IS NULL
       OR (created_at, id) < (sqlc.narg(before_created_at)::timestamptz, sqlc.narg(before_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT @page_limit;

-- name: ListSellerOrders :many
SELECT * FROM orders
WHERE seller_user_id = @seller_user_id
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(before_created_at)::timestamptz IS NULL
       OR (created_at, id) < (sqlc.narg(before_created_at)::timestamptz, sqlc.narg(before_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT @page_limit;

-- name: ListCheckoutOrders :many
SELECT * FROM orders WHERE checkout_id = @checkout_id ORDER BY id;

-- name: UpdateOrder :execrows
UPDATE orders
SET status = @status, cancel_reason = sqlc.narg(cancel_reason), payment_status = @payment_status,
    payment_deadline = sqlc.narg(payment_deadline), delivered_at = sqlc.narg(delivered_at),
    version = version + 1, updated_at = @updated_at
WHERE id = @id AND version = @version;

-- name: UpdatePaymentStatus :execrows
UPDATE orders
SET payment_status = @payment_status, version = version + 1, updated_at = @updated_at
WHERE id = @id AND version = @version;

-- name: ClaimStalePending :many
SELECT * FROM orders
WHERE status = 'PENDING' AND created_at < @created_before
ORDER BY created_at, id
LIMIT @batch_size
FOR UPDATE SKIP LOCKED;

-- name: ExpiredCheckouts :many
SELECT checkout_id, min(payment_deadline)::timestamptz AS deadline
FROM orders
WHERE status = 'AWAITING_PAYMENT' AND payment_deadline <= @now
GROUP BY checkout_id
HAVING sqlc.narg(after_deadline)::timestamptz IS NULL
    OR (min(payment_deadline), checkout_id) > (sqlc.narg(after_deadline)::timestamptz, sqlc.narg(after_checkout_id)::uuid)
ORDER BY deadline, checkout_id
LIMIT @batch_size;

-- name: LockAwaitingPayment :many
SELECT * FROM orders
WHERE checkout_id = @checkout_id AND status = 'AWAITING_PAYMENT' AND payment_deadline <= @now
ORDER BY id
FOR UPDATE SKIP LOCKED;

-- name: ClaimDueDelivered :many
SELECT * FROM orders
WHERE status = 'DELIVERED' AND delivered_at <= @delivered_before
ORDER BY delivered_at, id
LIMIT @batch_size
FOR UPDATE SKIP LOCKED;
