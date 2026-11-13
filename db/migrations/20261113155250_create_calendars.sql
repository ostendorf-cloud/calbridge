-- migrate:up

-- calendars is one calendar inside one connection: either an existing
-- calendar the user selected, or one calbridge created and owns
-- (managed = true), per docs/concepts/calbridge.md. Every calendar
-- belongs to exactly one connection and, through it, one user.
CREATE TABLE calendars (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users (id),
    connection_id uuid NOT NULL REFERENCES connections (id),
    -- The provider's own identifier for this calendar. Opaque and
    -- provider-shaped; internal/provider, a later step, is the only
    -- reader that interprets its format.
    external_id text NOT NULL,
    name text NOT NULL,
    -- True for a calendar calbridge created and owns as a sync target;
    -- false for an existing calendar the user selected. See
    -- docs/concepts/calbridge.md's "Decisions to record as ADRs".
    managed boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT calendars_external_id_not_empty CHECK (external_id <> ''),
    CONSTRAINT calendars_name_not_empty CHECK (name <> ''),
    -- Composite, not a plain `connection_id uuid REFERENCES connections
    -- (id)`: ties a calendar's user_id to the actual owner of the
    -- connection it belongs to, closing the cross-user existence oracle a
    -- single-column reference would open. See connections' own comment
    -- on connections_user_id_id_unique for the full reasoning, which
    -- applies here unchanged.
    CONSTRAINT calendars_user_id_connection_id_fkey FOREIGN KEY (user_id, connection_id) REFERENCES connections (user_id, id),
    -- Lets edges and grants (the next migrations) reference a calendar
    -- together with its owning user, the same technique
    -- connections_user_id_id_unique sets up for this table.
    CONSTRAINT calendars_user_id_id_unique UNIQUE (user_id, id)
);

-- One row per external calendar per connection: reconnecting or
-- re-listing calendars must not duplicate a calendar already tracked.
CREATE UNIQUE INDEX calendars_connection_id_external_id_idx ON calendars (connection_id, external_id);

ALTER TABLE calendars ENABLE ROW LEVEL SECURITY;
ALTER TABLE calendars FORCE ROW LEVEL SECURITY;

-- Same mechanism as identities and connections; see identities' own
-- comment for the full reasoning.
CREATE POLICY calendars_user_isolation ON calendars
    USING (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid)
    WITH CHECK (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid);

-- migrate:down

DROP TABLE calendars;
