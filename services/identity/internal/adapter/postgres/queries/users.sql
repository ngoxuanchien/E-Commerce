-- name: CreateUser :one
INSERT INTO users (id, email, password_hash, full_name)
VALUES ($1, $2, $3, $4)
RETURNING created_at;

-- name: AddUserRole :exec
INSERT INTO user_roles (user_id, role)
VALUES ($1, $2)
ON CONFLICT (user_id, role) DO NOTHING;

-- name: GetUserByEmail :one
SELECT id, email, password_hash, full_name, created_at
FROM users
WHERE email = $1;

-- name: GetUserByID :one
SELECT id, email, full_name, created_at
FROM users
WHERE id = $1;

-- name: ListUserRoles :many
SELECT role
FROM user_roles
WHERE user_id = $1
ORDER BY role;
