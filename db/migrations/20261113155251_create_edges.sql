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

-- Postgres does not index a foreign key column on its own; the grantee
-- lookup edges_check_calendar_access below runs on every insert and
-- update of either column, so both get one.
CREATE INDEX edges_source_calendar_id_idx ON edges (source_calendar_id);
CREATE INDEX edges_target_calendar_id_idx ON edges (target_calendar_id);

ALTER TABLE edges ENABLE ROW LEVEL SECURITY;
ALTER TABLE edges FORCE ROW LEVEL SECURITY;

-- Same mechanism as every earlier user-scoped table in this schema; see
-- identities' own comment for the full reasoning.
CREATE POLICY edges_user_isolation ON edges
    USING (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid)
    WITH CHECK (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid);

-- source_calendar_id and target_calendar_id above are plain references
-- to calendars (id), not the composite-foreign-key technique connections,
-- calendars and event_mapping use: Postgres checks a foreign key as the
-- table owner, bypassing row level security entirely, so a plain
-- reference lets any INSERT confirm whether a guessed calendar UUID
-- exists under some other user at all, with no grant required at the
-- database level - exactly the cross-user existence oracle the composite
-- technique exists to close elsewhere in this schema.
--
-- The composite technique itself does not carry over unchanged, though:
-- it ties a referencing row's own user_id straight to the referenced
-- row's owner, and an edge's user_id is deliberately not always the
-- owner of both calendars it names (see this table's own comment above -
-- a grantee builds an edge naming a calendar a grantor still owns).
-- Tying user_id straight to each calendar's owner would reject that
-- legitimate case along with the illegitimate one.
--
-- This trigger enforces the same shape of guarantee instead: a calendar
-- an edge names is only ever one the edge's own user_id owns directly,
-- or one a calendar_grants row actually shares with it, checked without
-- regard to detail level (calendar_grants.max_detail_level is
-- internal/sync's concern, a later step, not this migration's). It fires
-- before the plain foreign key is ever reached, so a nonexistent
-- calendar id and an existing one nobody shared with this user raise the
-- identical exception either way - closing the oracle rather than
-- reopening it by letting the two cases fail differently.
--
-- Deliberately SECURITY INVOKER, the default left unspecified: row
-- level security already restricts what the invoking, unprivileged
-- application role can see on calendars and calendar_grants to exactly
-- its own rows on either side (ownership or grant), which is exactly
-- what this check needs, so elevating its privileges would only let it
-- see more than the check requires, never less.
CREATE FUNCTION edges_check_calendar_access() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM calendars c
        WHERE c.id = NEW.source_calendar_id AND c.user_id = NEW.user_id
    ) AND NOT EXISTS (
        SELECT 1 FROM calendar_grants g
        WHERE g.calendar_id = NEW.source_calendar_id AND g.grantee_user_id = NEW.user_id
    ) THEN
        RAISE EXCEPTION 'edges: source_calendar_id is not owned by or granted to this edge''s user';
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM calendars c
        WHERE c.id = NEW.target_calendar_id AND c.user_id = NEW.user_id
    ) AND NOT EXISTS (
        SELECT 1 FROM calendar_grants g
        WHERE g.calendar_id = NEW.target_calendar_id AND g.grantee_user_id = NEW.user_id
    ) THEN
        RAISE EXCEPTION 'edges: target_calendar_id is not owned by or granted to this edge''s user';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER edges_check_calendar_access
    BEFORE INSERT OR UPDATE OF source_calendar_id, target_calendar_id, user_id ON edges
    FOR EACH ROW
    EXECUTE FUNCTION edges_check_calendar_access();

-- migrate:down

DROP TABLE edges;
DROP FUNCTION edges_check_calendar_access();
