-- migrate:up

-- A grant is only usable if both sides can say what it is about. Until
-- now neither could: calendars is policied by ownership alone, so a
-- grantee cannot read the name of the calendar shared with them, and
-- users is policied by a row's own id, so neither side can read the
-- other's email address. The grant row itself carries two opaque uuids
-- and a detail level, which is not enough to render a sharing screen or
-- to label the node an edge is drawn from.
--
-- This migration opens exactly those two readings and nothing else. It
-- opens no new writing at all: every policy and function below is a
-- read.

-- The calendars half. calendars_user_isolation stays exactly as it is
-- and keeps deciding every command; this is a second, permissive policy
-- that only ever adds SELECT, and only for a calendar a calendar_grants
-- row actually shares with the current user.
--
-- Permissive policies combine with OR within one command type and with
-- AND across command types, so this widens what SELECT returns and
-- changes nothing about INSERT, UPDATE or DELETE: those still have to
-- satisfy calendars_user_isolation's own ownership test, which this
-- policy cannot weaken. A grantee therefore reads the shared calendar
-- and can do nothing else with it.
--
-- The disclosure is bounded by the grant and by nothing else: there is
-- no way to reach a calendar no grant names, so this opens no existence
-- oracle over calendars in general.
--
-- Row level security is row level, so what this admits, for a calendar
-- the owner deliberately shared, is every column of that row: id,
-- user_id, connection_id, external_id, name, managed, created_at and
-- updated_at. external_id is the one of those a grantee has no business
-- with. For a Google or Microsoft primary calendar it is the owner's
-- provider mailbox address, which is not necessarily the calbridge
-- account address the grant already tells the grantee. Everything else
-- on the row is strictly less than what the grant itself shares, which
-- is the calendar's events at a bounded detail level.
--
-- A policy cannot withhold one column, so the projection is what keeps
-- external_id in: ListCalendarsSharedWithMe (see
-- internal/storage/postgres/queries/calendars.sql) names the columns it
-- reads and leaves external_id out, and storage.SharedCalendar has no
-- field to put it in. That query also names the join to
-- calendar_grants explicitly, the same test this policy's own USING
-- clause makes, so the two layers agree the way every other query in
-- that directory and its policy already do.
CREATE POLICY calendars_grantee_read ON calendars
    FOR SELECT
    USING (
        EXISTS (
            SELECT 1
            FROM calendar_grants g
            WHERE g.calendar_id = calendars.id
              AND g.grantee_user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
        )
    );

-- The users half. A policy cannot do this one: users carries
-- password_hash and totp_secret, and row level security is row level,
-- so a policy admitting a counterparty's row would admit that row's
-- secrets with it, for every query that role ever runs. A SECURITY
-- DEFINER function is what keeps the disclosure to one named set of
-- rows reachable from one call, the same technique and the same
-- reasoning users_find_by_email already uses.
--
-- RETURNS SETOF users, matching users_find_by_email rather than a
-- narrower row type, because sqlc resolves a function's columns from
-- the table a SETOF names and cannot resolve them from a RETURNS TABLE
-- list at all (verified by running task generate:sqlc against both
-- spellings).
--
-- The rowtype being the whole of users does not mean the whole of a
-- users row leaves the table. The body below names every column
-- explicitly and substitutes NULL for the three credential columns, so
-- password_hash, totp_secret and totp_last_counter are not merely
-- unprojected by the caller's own query, they carry no value to
-- project. A query in internal/storage/postgres/queries that selected
-- them anyway would read NULL. users_find_by_email keeps the wide shape
-- legitimately, because checking a password needs the hash; this
-- function never does, so the columns stay empty rather than relying on
-- a projection one directory away to keep them in.
--
-- The column list and the casts have to match users exactly, in order:
-- a SETOF function's result is the table's composite type, so a missing
-- column or a type the composite does not accept fails at CREATE
-- FUNCTION time. See 20261113155247_create_users.sql for the shape the
-- list below mirrors.
--
-- SECURITY DEFINER runs this function as its owner, the role that
-- creates it by running this migration, which docker-compose.yaml's own
-- "two Postgres roles" comment and charts/calbridge/README.md's
-- "Migrations run as a separate role" section both require to be a
-- Postgres superuser or otherwise able to run db/migrations in the
-- first place, never the restricted role the application itself
-- connects as. A superuser bypasses row level security regardless of
-- FORCE, so this function sees every row on both tables it reads
-- regardless of who calls it. A deployment that runs migrations as a
-- non-superuser owner instead would have FORCE bind that owner on both
-- tables, so users_self_isolation and calendar_grants_user_isolation
-- would apply to this function's own queries on top of the WHERE
-- clause below, and it would quietly return fewer counterparties
-- rather than failing in a way that points at the cause. The WHERE
-- clause below is therefore the whole of the access control a
-- correctly deployed, superuser-owned migration role relies on, which
-- is why it names app.user_id on both sides of the grant rather than
-- taking a
-- parameter: a caller cannot ask about somebody else's grants, because
-- there is nothing to ask with. app.user_id itself is transaction-local
-- and set only by internal/storage/postgres's WithUser, so a caller
-- cannot forge it either. The NULLIF guard is what stops an unscoped
-- connection, where current_setting returns the empty string, from
-- matching anything.
--
-- It returns the counterparties of every grant the current user is on
-- either side of, which is exactly the set of people that user has
-- already either named themselves or been named by. search_path is
-- pinned so a caller cannot influence what "users" or "calendar_grants"
-- resolve to.
--
-- EXECUTE is granted to the application role separately, wherever that
-- role's other grants are made (docker-compose.yaml's "grant-app-role"
-- service; a cluster operator's own grant for charts/calbridge), never
-- inside this migration, the same placement every other grant in this
-- schema has.
CREATE FUNCTION grant_counterparties()
RETURNS SETOF users
LANGUAGE sql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
    SELECT u.id,
           u.email,
           NULL::text AS password_hash,
           NULL::text AS totp_secret,
           NULL::bigint AS totp_last_counter,
           u.status,
           u.created_at,
           u.updated_at
    FROM users u
    WHERE NULLIF(current_setting('app.user_id', true), '') IS NOT NULL
      AND EXISTS (
          SELECT 1
          FROM calendar_grants g
          WHERE (
                g.owner_user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
                AND g.grantee_user_id = u.id
            )
             OR (
                g.grantee_user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
                AND g.owner_user_id = u.id
            )
      );
$$;

-- PostgreSQL grants EXECUTE on a new function to PUBLIC by default, and
-- a SECURITY DEFINER function left that way is callable by every role in
-- the database. The same correction
-- 20261113155255_revoke_users_find_by_email_from_public.sql had to make
-- afterwards is made here in the migration that creates the function.
REVOKE EXECUTE ON FUNCTION grant_counterparties() FROM PUBLIC;

-- migrate:down

DROP FUNCTION grant_counterparties();
DROP POLICY calendars_grantee_read ON calendars;
