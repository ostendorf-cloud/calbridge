-- Same reasoning as identities.sql: user isolation is two independent
-- layers, the explicit WHERE clause here and row level security in
-- db/migrations. user_id names the edge's owner, not necessarily the
-- owner of source_calendar_id or target_calendar_id; see
-- db/migrations's own reasoning on the cross-user, grant-bounded case.

-- name: CreateEdge :one
INSERT INTO edges (user_id, source_calendar_id, target_calendar_id, detail_level)
VALUES ($1, $2, $3, $4)
RETURNING id, user_id, source_calendar_id, target_calendar_id, detail_level, created_at, updated_at;

-- name: GetEdgeByID :one
SELECT id, user_id, source_calendar_id, target_calendar_id, detail_level, created_at, updated_at
FROM edges
WHERE id = $1 AND user_id = NULLIF(current_setting('app.user_id', true), '')::uuid;

-- name: ListEdges :many
SELECT id, user_id, source_calendar_id, target_calendar_id, detail_level, created_at, updated_at
FROM edges
WHERE user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
ORDER BY created_at;

-- Edges that touch one calendar on either side. internal/sync asks this
-- when a grant over that calendar is revoked: the edges the grant covered
-- have to stop and give back what they wrote, and scanning every edge the
-- user owns to find them does not scale with the topology.

-- name: ListEdgesByCalendar :many
SELECT id, user_id, source_calendar_id, target_calendar_id, detail_level, created_at, updated_at
FROM edges
WHERE (source_calendar_id = $1 OR target_calendar_id = $1)
  AND user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
ORDER BY created_at;

-- name: UpdateEdgeDetailLevel :one
UPDATE edges
SET detail_level = $2,
    updated_at = now()
WHERE id = $1 AND user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
RETURNING id, user_id, source_calendar_id, target_calendar_id, detail_level, created_at, updated_at;

-- name: DeleteEdge :execrows
DELETE FROM edges
WHERE id = $1 AND user_id = NULLIF(current_setting('app.user_id', true), '')::uuid;
