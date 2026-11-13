-- Same two-layer reasoning as identities.sql, with one difference: a
-- grant has two legitimate readers, the owner and the grantee, matching
-- db/migrations's own row level security policy on calendar_grants. The
-- explicit WHERE clauses below mirror that OR exactly, as a second,
-- independent layer; CreateCalendarGrant still only ever takes
-- owner_user_id from the current scope, matching the policy's WITH
-- CHECK, which permits only the owner to create or change a grant.

-- name: CreateCalendarGrant :one
INSERT INTO calendar_grants (owner_user_id, grantee_user_id, calendar_id, max_detail_level)
VALUES ($1, $2, $3, $4)
RETURNING id, owner_user_id, grantee_user_id, calendar_id, max_detail_level, created_at;

-- name: GetCalendarGrantByID :one
SELECT id, owner_user_id, grantee_user_id, calendar_id, max_detail_level, created_at
FROM calendar_grants
WHERE id = $1
  AND (
    owner_user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
    OR grantee_user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
  );

-- name: ListCalendarGrants :many
SELECT id, owner_user_id, grantee_user_id, calendar_id, max_detail_level, created_at
FROM calendar_grants
WHERE owner_user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
   OR grantee_user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
ORDER BY created_at;

-- name: DeleteCalendarGrant :execrows
DELETE FROM calendar_grants
WHERE id = $1
  AND (
    owner_user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
    OR grantee_user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
  );
