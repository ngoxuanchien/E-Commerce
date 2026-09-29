-- name: InsertIdempotencyKey :execrows
INSERT INTO idempotency_keys (user_id, key, request_hash, created_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id, key) DO NOTHING;

-- name: GetIdempotencyKey :one
SELECT * FROM idempotency_keys WHERE user_id = $1 AND key = $2;

-- name: ReclaimIdempotencyKey :execrows
-- Takes over a key whose run died before storing a response: same request,
-- no response, claimed before @stale_before. created_at is the claim.
UPDATE idempotency_keys SET created_at = @claimed_at
WHERE user_id = @user_id AND key = @key AND request_hash = @request_hash
  AND response IS NULL AND created_at < @stale_before;

-- name: CompleteIdempotencyKey :execrows
UPDATE idempotency_keys SET response = @response, status_code = @status_code
WHERE user_id = @user_id AND key = @key AND created_at = @claimed_at;

-- name: DeleteIdempotencyKey :exec
DELETE FROM idempotency_keys WHERE user_id = $1 AND key = $2 AND created_at = $3;

-- name: DeleteIdempotencyKeysBefore :execrows
DELETE FROM idempotency_keys WHERE created_at < $1;
