-- name: ListCategories :many
-- Top-level rows first, then children grouped by parent, each by position.
SELECT id, parent_id, name, slug, position
FROM categories
ORDER BY parent_id NULLS FIRST, position, name;
