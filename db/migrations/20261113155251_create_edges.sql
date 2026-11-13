-- migrate:up

-- edges is a sync edge: copies events from one source calendar into one
-- target calendar in one direction, at a detail level the edge fixes.
-- user_id is the edge's owner, the person who built it: ordinarily the
-- same person who owns both calendars, but not always. A user grants
-- another user access to one of their calendars (see the next migration,
-- grants), and the grantee then builds the edge: source_calendar_id can
-- belong to the grantor, target_calendar_id to the grantee, and user_id
-- names the grantee, who owns the edge itself. Enforcing that such an
-- edge only exists where a matching grant actually permits it is
-- internal/sync's job, a later step; this migration only shapes the
-- table and its own row level security, by edge ownership, not by
-- calendar ownership.
CREATE TABLE edges (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users (id),
    source_calendar_id uuid NOT NULL REFERENCES calendars (id),
    target_calendar_id uuid NOT NULL REFERENCES calendars (id),
    -- Presets per docs/concepts/calbridge.md: full copy, title and time,
    -- busy block. A per-field override for a case a preset misses is
    -- internal/sync's concern, a later step; this column only ever holds
    -- the preset itself.
    detail_level text NOT NULL CHECK (detail_level IN ('full', 'title_time', 'busy_block')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT edges_source_target_distinct CHECK (source_calendar_id <> target_calendar_id),
    -- Lets event_mapping (the last migration) reference an edge together
    -- with its owning user, the same composite-foreign-key technique
    -- connections and calendars already use, for the same reason.
    CONSTRAINT edges_user_id_id_unique UNIQUE (user_id, id)
);

ALTER TABLE edges ENABLE ROW LEVEL SECURITY;
ALTER TABLE edges FORCE ROW LEVEL SECURITY;

-- Same mechanism as every earlier user-scoped table in this schema; see
-- identities' own comment for the full reasoning.
CREATE POLICY edges_user_isolation ON edges
    USING (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid)
    WITH CHECK (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid);

-- migrate:down

DROP TABLE edges;
