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
-- oracle over calendars in general. What it does disclose, for a
-- calendar the owner deliberately shared, is its name, its connection id
-- and whether calbridge manages it. That is strictly less than what the
-- grant already shares, which is the calendar's events at a bounded
-- detail level.
--
-- The grantee's own SELECT still goes through ListCalendarsSharedWithMe
-- (see internal/storage/postgres/queries/calendars.sql), which names the
-- join to calendar_grants explicitly, so the two layers agree the way
-- every other query in that directory and its policy already do.
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
-- spellings). What the caller actually reads back is the projection its
-- own query makes: ListGrantCounterparties selects id and email and
-- nothing else, so no hash and no TOTP secret leaves the database for a
-- sharing screen. That is the same shape GetUserByEmail already has,
-- and narrower, since that one selects the whole row.
--
-- SECURITY DEFINER means the body runs as this migration's owner and so
-- bypasses row level security on both tables it reads. The WHERE clause
-- below is therefore the whole of the access control, which is why it
-- names app.user_id on both sides of the grant rather than taking a
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
    SELECT u.*
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
