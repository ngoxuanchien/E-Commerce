-- name: InsertIdempotencyKey :execrows
INSERT INTO idempotency_keys (user_id, key, request_hash, created_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id, key) DO NOTHING;

-- name: GetIdempotencyKey :one
SELECT * FROM idempotency_keys WHERE user_id = $1 AND key = $2;

-- name: CompleteIdempotencyKey :exec
UPDATE idempotency_keys SET response = @response, status_code = @status_code
WHERE user_id = @user_id AND key = @key;

-- name: DeleteIdempotencyKey :exec
DELETE FROM idempotency_keys WHERE user_id = $1 AND key = $2;

-- name: DeleteIdempotencyKeysBefore :execrows
DELETE FROM idempotency_keys WHERE created_at < $1;
