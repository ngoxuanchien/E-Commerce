-- name: InsertPayout :exec
INSERT INTO payouts (id, seller_user_id, amount, status, requested_at, version)
VALUES (@id, @seller_user_id, @amount, @status, @requested_at, @version);

-- name: ClaimDuePayout :one
SELECT * FROM payouts
WHERE status = 'REQUESTED' AND requested_at <= @due
ORDER BY requested_at, id
LIMIT 1
FOR UPDATE SKIP LOCKED;

-- name: FinishPayout :execrows
UPDATE payouts
SET status = @status, failure_reason = sqlc.narg(failure_reason), settled_at = sqlc.narg(settled_at), version = @version
WHERE id = @id AND version = @prev_version;
