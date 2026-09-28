-- name: CreateRefreshToken :exec
INSERT INTO refresh_tokens (id, user_id, family_id, token_hash, expires_at)
VALUES ($1, $2, $3, $4, $5);

-- name: GetRefreshTokenForUpdate :one
SELECT id, user_id, family_id, token_hash, expires_at, revoked_at
FROM refresh_tokens
WHERE token_hash = $1
FOR UPDATE;

-- name: ReplaceRefreshToken :exec
UPDATE refresh_tokens
SET revoked_at = @revoked_at, replaced_by = @replaced_by
WHERE id = @id;

-- name: RevokeRefreshTokenFamily :exec
UPDATE refresh_tokens
SET revoked_at = @revoked_at
WHERE family_id = @family_id AND revoked_at IS NULL;

-- name: RevokeRefreshToken :exec
UPDATE refresh_tokens
SET revoked_at = @revoked_at
WHERE token_hash = @token_hash AND revoked_at IS NULL;
