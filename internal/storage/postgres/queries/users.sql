-- users carries row level security scoped to a row's own id (see
-- db/migrations). GetUserByEmail is the only query here that runs
-- against the pool directly rather than inside a WithUser transaction:
-- the email lookup has to work before any user id is known at all, which
-- is exactly what its users_find_by_email call, a SECURITY DEFINER
-- function, is for. Every other query here, CreateUser included, runs
-- inside a WithUser transaction like every other repository.

-- CreateUser takes the new user's id as a parameter instead of leaving
-- it to the column's DEFAULT: signup scopes its own transaction to the
-- user it is about to create, so the id has to exist before the INSERT
-- does. RETURNING reads the new row back, and a row read back has to
-- satisfy the table's SELECT policy too, which is what that scope
-- satisfies; see db/migrations.
-- name: CreateUser :one
INSERT INTO users (id, email, password_hash, totp_secret, totp_last_counter, status)
VALUES ($1, $2, $3, $4, $5, $6)
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
