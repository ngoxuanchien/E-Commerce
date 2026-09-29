-- name: InsertProviderEvent :execrows
INSERT INTO provider_events (provider, event_key, received_at) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING;

-- name: PurgeProviderEventsBefore :execrows
DELETE FROM provider_events WHERE received_at < $1;

-- name: RecordCancellation :execrows
INSERT INTO order_cancellations (order_id, checkout_id, cancelled_at) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING;

-- name: IsCancelled :one
SELECT EXISTS (SELECT 1 FROM order_cancellations WHERE order_id = $1);
