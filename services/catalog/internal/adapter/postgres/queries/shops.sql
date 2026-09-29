-- name: InsertShop :one
INSERT INTO shops (id, owner_user_id, name, slug, description)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetShopBySlug :one
SELECT * FROM shops WHERE slug = $1;

-- name: GetShopByOwner :one
SELECT * FROM shops WHERE owner_user_id = $1;

-- name: UpdateShop :one
UPDATE shops SET
    name        = coalesce(sqlc.narg('name'), name),
    description = coalesce(sqlc.narg('description'), description),
    updated_at  = now()
WHERE owner_user_id = sqlc.arg('owner_user_id')
RETURNING *;
