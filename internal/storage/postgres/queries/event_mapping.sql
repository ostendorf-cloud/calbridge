-- Same reasoning as identities.sql: user isolation is two independent
-- layers, the explicit WHERE clause here and row level security in
-- db/migrations. encrypted_details travels through these queries
-- already encrypted: internal/storage/postgres's event_mapping.go
-- encrypts it before Create and UpdateEventMapping, and decrypts it
-- after every read, so this file only ever handles opaque bytes.

-- name: CreateEventMapping :one
INSERT INTO event_mapping (user_id, edge_id, source_event_id, target_event_id, starts_at, ends_at, content_hash, encrypted_details)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id, user_id, edge_id, source_event_id, target_event_id, starts_at, ends_at, content_hash, encrypted_details, created_at, updated_at;

-- name: GetEventMappingByID :one
SELECT id, user_id, edge_id, source_event_id, target_event_id, starts_at, ends_at, content_hash, encrypted_details, created_at, updated_at
FROM event_mapping
WHERE id = $1 AND user_id = NULLIF(current_setting('app.user_id', true), '')::uuid;

-- name: GetEventMappingByEdgeAndSourceEventID :one
SELECT id, user_id, edge_id, source_event_id, target_event_id, starts_at, ends_at, content_hash, encrypted_details, created_at, updated_at
FROM event_mapping
WHERE edge_id = $1 AND source_event_id = $2 AND user_id = NULLIF(current_setting('app.user_id', true), '')::uuid;

-- Every event one edge wrote. internal/sync reads this on a full
-- reconcile, to find the copies whose source is gone, and on teardown, to
-- delete every event the edge ever wrote.

-- name: ListEventMappingsByEdge :many
SELECT id, user_id, edge_id, source_event_id, target_event_id, starts_at, ends_at, content_hash, encrypted_details, created_at, updated_at
FROM event_mapping
WHERE edge_id = $1 AND user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
ORDER BY source_event_id;

-- name: UpdateEventMapping :one
UPDATE event_mapping
SET target_event_id = $2,
    starts_at = $3,
    ends_at = $4,
    content_hash = $5,
    encrypted_details = $6,
    updated_at = now()
WHERE id = $1 AND user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
RETURNING id, user_id, edge_id, source_event_id, target_event_id, starts_at, ends_at, content_hash, encrypted_details, created_at, updated_at;

-- name: DeleteEventMapping :execrows
DELETE FROM event_mapping
WHERE id = $1 AND user_id = NULLIF(current_setting('app.user_id', true), '')::uuid;

-- Tearing an edge down removes its mapping rows in one statement, so the
-- edge row itself can be deleted in the same transaction without the
-- foreign key from event_mapping holding it back.

-- name: DeleteEventMappingsByEdge :execrows
DELETE FROM event_mapping
WHERE edge_id = $1 AND user_id = NULLIF(current_setting('app.user_id', true), '')::uuid;
