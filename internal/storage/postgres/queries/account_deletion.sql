-- Account deletion, per docs/concepts/calbridge.md and issue #5: the
-- account, its connections and everything its edges wrote. The queries
-- live together in one file rather than one per table because the order
-- they run in is the point: no table in this schema carries ON DELETE
-- CASCADE, deliberately, so a row is only ever removed by a statement
-- that says so, and the statements below have to run leaf first or a
-- foreign key refuses the one above it.
--
-- Every statement runs inside a WithUser transaction, so row level
-- security bounds each of them to the user being deleted even though
-- none of them names a user id of its own except through
-- current_setting. postgres.userRepository.DeleteAccount is the only
-- caller, and it runs them in exactly the order they appear here.

-- name: DeleteEventMappingsForUser :execrows
DELETE FROM event_mapping
WHERE user_id = NULLIF(current_setting('app.user_id', true), '')::uuid;

-- name: DeleteEdgesForUser :execrows
DELETE FROM edges
WHERE user_id = NULLIF(current_setting('app.user_id', true), '')::uuid;

-- Both sides, not only the owning one: a grant this user received from
-- somebody else references this user too, and the table's own policy
-- (see db/migrations) already admits either side to it.
-- name: DeleteCalendarGrantsForUser :execrows
DELETE FROM calendar_grants
WHERE owner_user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
   OR grantee_user_id = NULLIF(current_setting('app.user_id', true), '')::uuid;

-- name: DeleteCalendarsForUser :execrows
DELETE FROM calendars
WHERE user_id = NULLIF(current_setting('app.user_id', true), '')::uuid;

-- name: DeleteConnectionsForUser :execrows
DELETE FROM connections
WHERE user_id = NULLIF(current_setting('app.user_id', true), '')::uuid;

-- name: DeleteIdentitiesForUser :execrows
DELETE FROM identities
WHERE user_id = NULLIF(current_setting('app.user_id', true), '')::uuid;

-- The row itself, last. users is policied on its own id, so the scope
-- the transaction already carries is what makes this reach exactly one
-- row and no other.
-- name: DeleteUser :execrows
DELETE FROM users
WHERE id = NULLIF(current_setting('app.user_id', true), '')::uuid;
