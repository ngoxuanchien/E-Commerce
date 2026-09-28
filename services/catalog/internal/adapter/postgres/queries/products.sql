-- name: InsertProduct :exec
INSERT INTO products (id, shop_id, category_id, name, description, status)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: LockShopProduct :one
SELECT id FROM products WHERE id = $1 AND shop_id = $2 FOR UPDATE;

-- name: UpdateProductFields :exec
UPDATE products SET
    category_id = coalesce(sqlc.narg('category_id'), category_id),
    name        = coalesce(sqlc.narg('name'), name),
    description = coalesce(sqlc.narg('description'), description),
    status      = coalesce(sqlc.narg('status'), status),
    updated_at  = now()
WHERE id = sqlc.arg('id');

-- name: SetMinPrice :exec
UPDATE products SET min_price = $2 WHERE id = $1;

-- name: ArchiveProduct :execrows
UPDATE products SET status = 'archived', updated_at = now()
WHERE id = $1 AND shop_id = $2;

-- name: DeleteProductImages :exec
DELETE FROM product_images WHERE product_id = $1;

-- name: InsertProductImage :exec
INSERT INTO product_images (id, product_id, object_key, position) VALUES ($1, $2, $3, $4);

-- name: ListProductSkus :many
SELECT id, name, price, stock, active FROM skus WHERE product_id = $1 ORDER BY created_at, id;

-- name: InsertSku :exec
INSERT INTO skus (id, product_id, name, price, stock, active) VALUES ($1, $2, $3, $4, $5, $6);

-- name: UpdateSku :exec
UPDATE skus SET name = $3, price = $4, stock = $5, active = $6, updated_at = now()
WHERE id = $1 AND product_id = $2;

-- name: DeactivateSku :exec
UPDATE skus SET active = false, updated_at = now() WHERE id = $1 AND active;

-- name: GetProducts :many
SELECT p.id, p.shop_id, p.category_id, p.name, p.description, p.status, p.min_price,
       p.created_at, p.updated_at, s.name AS shop_name, s.slug AS shop_slug, s.owner_user_id
FROM products p JOIN shops s ON s.id = p.shop_id
WHERE p.id = ANY(sqlc.arg('ids')::uuid[]);

-- name: GetProductsSkus :many
SELECT id, product_id, name, price, stock, active FROM skus
WHERE product_id = ANY(sqlc.arg('ids')::uuid[])
ORDER BY created_at, id;

-- name: GetProductsImages :many
SELECT product_id, object_key FROM product_images
WHERE product_id = ANY(sqlc.arg('ids')::uuid[])
ORDER BY position;

-- name: ListShopProductIDs :many
SELECT id, created_at FROM products
WHERE shop_id = sqlc.arg('shop_id')
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
  AND (sqlc.narg('after_created_at')::timestamptz IS NULL
       OR (created_at, id) < (sqlc.narg('after_created_at')::timestamptz, sqlc.narg('after_id')::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('max_rows');
