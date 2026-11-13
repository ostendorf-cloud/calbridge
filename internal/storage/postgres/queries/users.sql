-- users carries no row level security: a row here IS a user, not
-- something scoped by one. These queries run against the pool directly,
-- never inside a WithUser transaction.

-- name: CreateUser :one
INSERT INTO users (email, password_hash, totp_secret, totp_last_counter, status)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, email, password_hash, totp_secret, totp_last_counter, status, created_at, updated_at;

-- name: GetUserByID :one
SELECT id, email, password_hash, totp_secret, totp_last_counter, status, created_at, updated_at
FROM users
WHERE id = $1;

-- name: GetUserByEmail :one
SELECT id, email, password_hash, totp_secret, totp_last_counter, status, created_at, updated_at
FROM users
WHERE lower(email) = lower(sqlc.arg(email));

-- name: UpdateUser :one
UPDATE users
SET password_hash = $2,
    totp_secret = $3,
    totp_last_counter = $4,
    status = $5,
    updated_at = now()
WHERE id = $1
RETURNING id, email, password_hash, totp_secret, totp_last_counter, status, created_at, updated_at;
