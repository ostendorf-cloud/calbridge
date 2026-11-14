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

-- ListGrantCounterparties answers the one question a sharing screen
-- cannot be built without: which person is on the other side of each
-- grant this user is party to. users is policied by a row's own id, so
-- neither side can read the other's row at all; grant_counterparties is
-- the SECURITY DEFINER function that discloses the address, and only the
-- address, for exactly the people already named by a grant. See
-- db/migrations for why a policy could not do this and what the function
-- bounds itself to.
--
-- It takes no parameter: the function reads app.user_id itself, so there
-- is nothing here that could ask about somebody else's grants.
--
-- The column list is id and email and nothing else. The function returns
-- SETOF users for sqlc's benefit (see db/migrations for why), so this
-- projection is what actually decides that no password hash and no TOTP
-- secret travels out of the database for a sharing screen.

-- name: ListGrantCounterparties :many
SELECT id, email
FROM grant_counterparties();

-- name: DeleteCalendarGrant :execrows
DELETE FROM calendar_grants
WHERE id = $1
  AND (
    owner_user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
    OR grantee_user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
  );
