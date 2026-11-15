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

-- ListCalendarsSharedWithMe is the other half of ListCalendars: the
-- calendars somebody else owns and shared with this user through a
-- calendar_grants row. It is a separate query rather than an OR inside
-- ListCalendars because the two answer different questions and a caller
-- that confuses them would, for instance, offer somebody else's calendar
-- as a target calbridge may create a calendar in.
--
-- The join to calendar_grants is the same test
-- calendars_grantee_read's own USING clause makes (see db/migrations),
-- the second independent layer every query in this directory carries.
-- max_detail_level and the owner come back with the row: a shared
-- calendar is only ever usable up to the level its grant allows, and
-- the two are read together so nothing above can hold one without the
-- other.
--
-- external_id is the one column of calendars this projection leaves
-- out, unlike ListCalendars above. The policy admits it, because row
-- level security is row level, but a grantee has no business with it:
-- for a Google or Microsoft primary calendar it is the owner's provider
-- mailbox address, which need not be the calbridge account address the
-- grant already names. This projection and storage.SharedCalendar's own
-- field list are what keep it in the table; see the policy's own
-- comment in db/migrations.

-- name: ListCalendarsSharedWithMe :many
SELECT c.id, c.user_id, c.connection_id, c.name, c.managed, c.created_at, c.updated_at,
       g.id AS grant_id, g.max_detail_level
FROM calendars c
JOIN calendar_grants g ON g.calendar_id = c.id
WHERE g.grantee_user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
ORDER BY c.created_at;

-- name: DeleteCalendar :execrows
DELETE FROM calendars
WHERE id = $1 AND user_id = NULLIF(current_setting('app.user_id', true), '')::uuid;
