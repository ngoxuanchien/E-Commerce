-- name: LockOrder :exec
-- Serializes every stock transaction of one order (idempotency, WS2 §3).
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg('order_id')::text, 0));

-- name: OrderHasReservations :one
SELECT EXISTS (SELECT 1 FROM stock_reservations WHERE order_id = $1);

-- name: DecrementStock :execrows
UPDATE skus s SET stock = s.stock - sqlc.arg('qty')::int, updated_at = now()
FROM products p
WHERE s.id = sqlc.arg('sku_id') AND p.id = s.product_id AND p.status = 'active'
  AND s.active AND s.stock >= sqlc.arg('qty')::int;

-- name: AvailableStock :one
SELECT (CASE WHEN s.active AND p.status = 'active' THEN s.stock ELSE 0 END)::int AS available
FROM skus s JOIN products p ON p.id = s.product_id
WHERE s.id = $1;

-- name: InsertReservation :exec
INSERT INTO stock_reservations (order_id, sku_id, quantity, status) VALUES ($1, $2, $3, 'reserved');

-- name: LockReservedForOrder :many
SELECT sku_id, quantity FROM stock_reservations
WHERE order_id = $1 AND status = 'reserved'
ORDER BY sku_id
FOR UPDATE;

-- name: IncrementStock :exec
UPDATE skus SET stock = stock + sqlc.arg('qty')::int, updated_at = now() WHERE id = sqlc.arg('sku_id');

-- name: MarkReleased :exec
UPDATE stock_reservations SET status = 'released', updated_at = now()
WHERE order_id = $1 AND status = 'reserved';

-- name: InsertProcessedEvent :execrows
INSERT INTO processed_events (event_id) VALUES ($1) ON CONFLICT DO NOTHING;

-- name: GetSkuInfos :many
SELECT s.id AS sku_id, s.name AS variant_name, s.price, s.stock, s.active AS sku_active,
       p.id AS product_id, p.name AS product_name, p.status AS product_status,
       sh.id AS shop_id, sh.name AS shop_name, sh.slug AS shop_slug, sh.owner_user_id,
       coalesce((SELECT i.object_key FROM product_images i WHERE i.product_id = p.id
                 ORDER BY i.position LIMIT 1), '')::text AS image_key
FROM skus s
JOIN products p ON p.id = s.product_id
JOIN shops sh ON sh.id = p.shop_id
WHERE s.id = ANY(sqlc.arg('ids')::uuid[]);
