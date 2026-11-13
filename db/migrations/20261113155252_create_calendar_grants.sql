-- migrate:up

-- calendar_grants is its own record (owner, grantee, calendar, maximum
-- detail level), per docs/concepts/calbridge.md: a user grants another
-- user access to one of their calendars at a maximum detail level, and
-- the grantee's edges are bounded by it. Named calendar_grants, not the
-- shorter "grants" the concept itself uses, because GRANT is a reserved
-- word in PostgreSQL's own grammar and cannot name a table unquoted.
-- Checking that an edge actually stays within its grant's maximum detail
-- level is internal/sync's job, a later step; this migration only shapes
-- the record and ties it to a calendar the owner actually owns.
CREATE TABLE calendar_grants (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_user_id uuid NOT NULL REFERENCES users (id),
    grantee_user_id uuid NOT NULL REFERENCES users (id),
    calendar_id uuid NOT NULL REFERENCES calendars (id),
    max_detail_level text NOT NULL CHECK (max_detail_level IN ('full', 'title_time', 'busy_block')),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT calendar_grants_owner_grantee_distinct CHECK (owner_user_id <> grantee_user_id),
    -- Composite, the same technique calendars_user_id_connection_id_fkey
    -- uses: ties calendar_id to a calendar the stated owner_user_id
    -- actually owns, so a grant can never be created, maliciously or by
    -- mistake, naming a calendar that belongs to someone else.
    CONSTRAINT calendar_grants_owner_user_id_calendar_id_fkey FOREIGN KEY (owner_user_id, calendar_id) REFERENCES calendars (user_id, id),
    CONSTRAINT calendar_grants_owner_grantee_calendar_unique UNIQUE (owner_user_id, grantee_user_id, calendar_id)
);

-- Postgres does not index a foreign key column on its own; ListCalendarGrants'
-- own grantee lookup, and edges_check_calendar_access (see db/migrations's
-- edges migration), both filter on this column.
CREATE INDEX calendar_grants_grantee_user_id_idx ON calendar_grants (grantee_user_id);

ALTER TABLE calendar_grants ENABLE ROW LEVEL SECURITY;
ALTER TABLE calendar_grants FORCE ROW LEVEL SECURITY;

-- A grant has two legitimate readers, not one: the owner, who created it,
-- and the grantee, who needs to see what has been shared with them to
-- build an edge against it. USING therefore matches either side; WITH
-- CHECK only the owner side, since only the owner of a calendar may
-- create or change a grant over it. The same NULLIF handling as every
-- other policy in this schema; see identities' own comment for why.
CREATE POLICY calendar_grants_user_isolation ON calendar_grants
    USING (
        owner_user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
        OR grantee_user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
    )
    WITH CHECK (owner_user_id = NULLIF(current_setting('app.user_id', true), '')::uuid);

-- migrate:down

DROP TABLE calendar_grants;
