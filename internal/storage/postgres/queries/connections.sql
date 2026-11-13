-- Same reasoning as identities.sql: user isolation is two independent
-- layers, the explicit WHERE clause here and row level security in
-- db/migrations. credentials travels through these queries already
-- encrypted: internal/storage/postgres's connections.go encrypts it
-- before Create and UpdateConnectionCredentials, and decrypts it after
-- every read, so this file only ever handles opaque bytes.

-- name: CreateConnection :one
INSERT INTO connections (user_id, provider, display_name, credentials)
VALUES ($1, $2, $3, $4)
RETURNING id, user_id, provider, display_name, credentials, created_at, updated_at;

-- name: GetConnectionByID :one
SELECT id, user_id, provider, display_name, credentials, created_at, updated_at
FROM connections
WHERE id = $1 AND user_id = NULLIF(current_setting('app.user_id', true), '')::uuid;

-- name: ListConnections :many
SELECT id, user_id, provider, display_name, credentials, created_at, updated_at
FROM connections
WHERE user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
ORDER BY created_at;

-- name: UpdateConnectionCredentials :one
UPDATE connections
SET credentials = $2,
    updated_at = now()
WHERE id = $1 AND user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
RETURNING id, user_id, provider, display_name, credentials, created_at, updated_at;

-- name: DeleteConnection :execrows
DELETE FROM connections
WHERE id = $1 AND user_id = NULLIF(current_setting('app.user_id', true), '')::uuid;
