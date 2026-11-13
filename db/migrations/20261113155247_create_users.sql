-- migrate:up

-- users is the root of per-user isolation: every other user-owned table
-- carries a user_id referencing this one. A row here IS a user rather
-- than something owned by one, so its own policy, below, scopes by id
-- directly instead of by a user_id column it has none of.
--
-- Anyone can create an account themselves, per docs/concepts/calbridge.md,
-- so there is no tenant or invite step in front of signup to resolve
-- first, and the one flow that has to run before any user id is known -
-- looking a local account up by email to check its password - stays
-- possible without opening SELECT to every row; see users_find_by_email
-- below for how.
--
-- Local credentials live on this table directly, the same way solobill's
-- accounts table holds them, rather than in a separate table: a local
-- login is a property of a user, not a distinct entity with its own
-- lifecycle here. Provider sign-in identities are a separate table
-- (db/migrations/*_create_identities.sql): a user may have a password, one
-- or more sign-in identities, both, or neither (an identity-only account
-- that never sets a password).
CREATE TABLE users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email text NOT NULL,
    -- Nullable: an account created through provider sign-in alone never
    -- sets a password. Holds an already-computed hash string; hashing
    -- itself is internal/auth's job, not this one's.
    password_hash text,
    -- Nullable until TOTP is enrolled. Stored as the base32 secret text a
    -- TOTP library issues and verifies against directly, the same shape
    -- and the same reasoning as solobill's accounts.totp_secret: it saves
    -- a bytea<->base32 round trip on every login check, the common case,
    -- at the cost of one on enrollment, the rare case.
    totp_secret text,
    -- Nullable: set once a login succeeds with a valid TOTP code, to the
    -- RFC 6238 counter that code matched, so a later login can refuse a
    -- replay of the exact same code. internal/auth, a later step, is the
    -- only writer.
    totp_last_counter bigint,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_email_not_empty CHECK (email <> ''),
    CONSTRAINT users_totp_secret_length CHECK (totp_secret IS NULL OR length(totp_secret) = 32)
);

-- Case-insensitive and global: calbridge has no tenant to scope email
-- uniqueness by, unlike solobill's per-tenant accounts_tenant_id_email_lower_idx.
CREATE UNIQUE INDEX users_email_lower_idx ON users (lower(email));

-- Same reasoning as every other table in this schema: ENABLE alone
-- exempts the table's owning role, so FORCE is what actually binds the
-- role that runs migrations and, unless charts/calbridge's
-- migrationJob.databaseUser is set to a second role, the role the
-- application connects as too.
ALTER TABLE users ENABLE ROW LEVEL SECURITY;
ALTER TABLE users FORCE ROW LEVEL SECURITY;

-- Signup is the one write that runs before the user it writes exists,
-- but it is still scoped rather than exempt: internal/storage/postgres
-- generates the new user's id in Go, before the INSERT, and scopes the
-- signup transaction to that id, so the row being written is exactly the
-- row app.user_id names and the single policy below covers signup like
-- every other command. The column keeps its own DEFAULT for a row
-- inserted by hand.
--
-- An INSERT ... RETURNING needs exactly that. A FOR INSERT policy only
-- ever contributes to the INSERT WITH CHECK, and RETURNING reads the new
-- row back, which is a read: per CREATE POLICY, "if a data-modifying
-- query has a RETURNING clause, SELECT permissions are required on the
-- relation, and any newly inserted or updated rows from the relation
-- must satisfy the relation's SELECT policies in order to be available
-- to the RETURNING clause", and its "Policies Applied by Command Type"
-- table lists the SELECT/ALL policy's USING expression as checking the
-- new row of an INSERT whenever read access to that row is required. So
-- an unconditional FOR INSERT policy would let the row be written and
-- still not let signup read it back. Only a scope the SELECT policy
-- itself accepts does both. Keeping the one policy, with no FOR INSERT
-- escape hatch beside it, also means no row can be written here at all
-- except under the scope of the very user it creates.

-- A row here IS a user, so "owned by" collapses to "is": every command
-- is scoped to the row's own id. Same NULLIF handling as every other
-- policy in this schema; see identities' own migration for the full
-- reasoning. This also blocks the pre-login email lookup, which runs
-- before any user id is known. See users_find_by_email below for the
-- one, auditable way around that.
CREATE POLICY users_self_isolation ON users
    USING (id = NULLIF(current_setting('app.user_id', true), '')::uuid)
    WITH CHECK (id = NULLIF(current_setting('app.user_id', true), '')::uuid);

-- The one bypass of the policy above: SECURITY DEFINER runs this
-- function as its owner, the role that creates it by running this
-- migration, which docker-compose.yaml's own "two Postgres roles"
-- comment and charts/calbridge/README.md's "Migrations run as a
-- separate role" section both require to be a Postgres superuser or
-- otherwise able to run db/migrations in the first place, never the
-- restricted role the application itself connects as. A superuser
-- bypasses row level security regardless of FORCE, so this function
-- sees every row regardless of who calls it. RETURNS SETOF users, not a
-- hand-picked column list: a login check needs most of the row anyway
-- (password_hash, totp_secret, totp_last_counter, status alongside id),
-- and returning the same composite type GetUserByID and UpdateUser
-- already return keeps the one function auditable without a second,
-- narrower User shape to maintain. search_path is pinned so a caller
-- cannot influence what "users" or "lower" resolve to.
--
-- EXECUTE is granted to the application role separately, wherever that
-- role's other table grants are made (docker-compose.yaml's
-- "grant-app-role" service; a cluster operator's own grant for
-- charts/calbridge), never inside this migration: the role this
-- function is meant for does not necessarily exist yet when migrations
-- run, the same reason every other table-level GRANT in this schema
-- lives outside db/migrations too.
CREATE FUNCTION users_find_by_email(p_email text)
RETURNS SETOF users
LANGUAGE sql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
    SELECT *
    FROM users
    WHERE lower(email) = lower(p_email);
$$;

-- migrate:down

DROP FUNCTION users_find_by_email(text);
DROP TABLE users;
