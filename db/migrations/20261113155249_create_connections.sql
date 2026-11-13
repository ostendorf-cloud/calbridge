-- migrate:up

-- connections is a provider account connected as a calendar: an OAuth
-- grant for Google or Microsoft, or an Apple ID and app-specific password
-- for iCloud over CalDAV, per docs/concepts/calbridge.md. It is a separate
-- table from identities (the previous migration), with no foreign key or
-- other reference between the two: see that migration's own comment for
-- why. Connecting a calendar is a second, explicit consent step after
-- signing in, asking for calendar scopes the identity never asked for.
CREATE TABLE connections (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users (id),
    provider text NOT NULL CHECK (provider IN ('google', 'microsoft', 'caldav')),
    -- A human-readable label for this connection, shown in the UI so a
    -- user with several connected accounts can tell them apart. Not used
    -- for lookup or uniqueness.
    display_name text NOT NULL DEFAULT '',
    -- Opaque, encrypted bytes: an OAuth refresh token for Google and
    -- Microsoft, or an Apple ID plus app-specific password for a CalDAV
    -- connection, encrypted at rest with a key the process reads from the
    -- environment. internal/storage/postgres applies the encryption and
    -- decryption on every write and read of this column, so no caller of
    -- ConnectionRepository ever has to remember to do it; see
    -- internal/storage/postgres/crypto.go. The typed credential shape
    -- itself belongs to internal/provider, a later step: this column only
    -- ever holds already-serialized, already-encrypted bytes.
    credentials bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT connections_provider_not_empty CHECK (provider <> ''),
    -- Lets calendars (the next migration) reference a connection together
    -- with its owning user, so its own foreign key can enforce that a
    -- calendar's user_id always matches the connection it belongs to,
    -- the same composite-foreign-key technique solobill's invites
    -- migration uses on accounts for exactly the same reason: Postgres
    -- checks referential integrity as the table owner, bypassing row
    -- level security entirely, so a plain `connection_id uuid REFERENCES
    -- connections (id)` on calendars would be a cross-user existence
    -- oracle otherwise.
    CONSTRAINT connections_user_id_id_unique UNIQUE (user_id, id)
);

ALTER TABLE connections ENABLE ROW LEVEL SECURITY;
ALTER TABLE connections FORCE ROW LEVEL SECURITY;

-- Same mechanism as identities; see that migration's own comment.
CREATE POLICY connections_user_isolation ON connections
    USING (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid)
    WITH CHECK (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid);

-- migrate:down

DROP TABLE connections;
