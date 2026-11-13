-- migrate:up

-- users is the root of per-user isolation: every other user-owned table
-- carries a user_id referencing this one. It deliberately carries no row
-- level security policy of its own, because it has no user_id column to
-- scope by: a row here IS a user, not something owned by one.
--
-- A self-referential policy such as "id = current_setting('app.user_id')"
-- would also break the one flow that has to run before a user is known:
-- looking a local account up by email to check its password, which is
-- what then produces the user id passed to set_config for every later,
-- user-scoped query. Anyone can create an account themselves, per
-- docs/concepts/calbridge.md, so this lookup is unscoped by design, not a
-- gap: there is no tenant or invite step in front of it to resolve first.
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

-- migrate:down

DROP TABLE users;
