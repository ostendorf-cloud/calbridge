-- migrate:up

-- Provider sign-in has the same chicken and egg problem the local path
-- already has: resolving a (provider, subject) pair to a calbridge user
-- is the step that runs before any user id exists, and identities is
-- policied on user_id, so nothing can read it yet. users solved it with
-- users_find_by_email, a SECURITY DEFINER function; this is the same
-- bypass, with the same reasoning and the same auditability, for the
-- one lookup provider sign-in cannot do without.
--
-- The alternative, keying the lookup on the ID token's email claim so
-- it could go through users_find_by_email instead, was rejected: an
-- email can be reassigned to somebody else, which would bind a session
-- to whoever currently holds an address rather than to the account the
-- provider actually authenticated. govue's own oidc.Identity doc
-- comment is explicit about that.
--
-- SECURITY DEFINER runs this as its owner, the role that creates it by
-- running this migration, which is never the restricted role the
-- application connects as (see docker-compose.yaml's "two Postgres
-- roles" comment and charts/calbridge/README.md's "Migrations run as a
-- separate role" section). search_path is pinned so a caller cannot
-- influence what "identities" resolves to.
--
-- It reads identities and nothing else. It does not join connections, it
-- does not read connections, and no future change to it may: a provider
-- identity used to sign in is never matched against a provider account
-- another user connected as a calendar, because controlling a mailbox
-- whose calendar someone connected would otherwise be a path into that
-- person's calbridge account. The two tables carry no reference to each
-- other for the same reason (see the identities migration), and
-- internal/storage/postgres's own integration test asserts that this
-- function's body names no other table.
--
-- EXECUTE is granted to the application role wherever that role's other
-- grants are made (docker-compose.yaml's "grant-app-role" service; a
-- cluster operator's own grant for charts/calbridge), never here: the
-- role does not necessarily exist yet when migrations run.
CREATE FUNCTION identities_find_by_provider_subject(p_provider text, p_subject text)
RETURNS SETOF identities
LANGUAGE sql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
    SELECT *
    FROM identities
    WHERE provider = p_provider AND subject = p_subject;
$$;

-- migrate:down

DROP FUNCTION identities_find_by_provider_subject(text, text);
