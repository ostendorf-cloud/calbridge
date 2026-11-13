-- users carries row level security scoped to a row's own id (see
-- db/migrations). CreateUser and GetUserByEmail run against the pool
-- directly, never inside a WithUser transaction: signup has no existing
-- self to scope by, and the email lookup has to work before any user id
-- is known at all, which is exactly what GetUserByEmail's
-- users_find_by_email call, a SECURITY DEFINER function, is for. Every
-- other query here runs inside a WithUser transaction like every other
-- repository.

-- name: CreateUser :one
INSERT INTO users (email, password_hash, totp_secret, totp_last_counter, status)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, email, password_hash, totp_secret, totp_last_counter, status, created_at, updated_at;

-- name: GetUserByID :one
SELECT id, email, password_hash, totp_secret, totp_last_counter, status, created_at, updated_at
FROM users
WHERE id = $1;

-- name: GetUserByEmail :one
SELECT *
FROM users_find_by_email(sqlc.arg(email));

-- name: UpdateUser :one
UPDATE users
SET password_hash = $2,
    totp_secret = $3,
    totp_last_counter = $4,
    status = $5,
    updated_at = now()
WHERE id = $1
RETURNING id, email, password_hash, totp_secret, totp_last_counter, status, created_at, updated_at;
