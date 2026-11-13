-- Same reasoning as identities.sql: user isolation is two independent
-- layers, the explicit WHERE clause here and row level security in
-- db/migrations.

-- name: CreateCalendar :one
INSERT INTO calendars (user_id, connection_id, external_id, name, managed)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, user_id, connection_id, external_id, name, managed, created_at, updated_at;

-- name: GetCalendarByID :one
SELECT id, user_id, connection_id, external_id, name, managed, created_at, updated_at
FROM calendars
WHERE id = $1 AND user_id = NULLIF(current_setting('app.user_id', true), '')::uuid;

-- name: ListCalendars :many
SELECT id, user_id, connection_id, external_id, name, managed, created_at, updated_at
FROM calendars
WHERE user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
ORDER BY created_at;

-- name: DeleteCalendar :execrows
DELETE FROM calendars
WHERE id = $1 AND user_id = NULLIF(current_setting('app.user_id', true), '')::uuid;
