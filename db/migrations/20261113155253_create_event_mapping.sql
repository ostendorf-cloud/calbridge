-- migrate:up

-- event_mapping is the sync engine's mapping table, per
-- docs/concepts/calbridge.md: edge id, source event id, target event id,
-- start, end and a content hash, so the database alone does not read as
-- a calendar. Any copied title or body is cached here encrypted at rest,
-- never in clear: encrypted_details holds that cache, nullable because a
-- busy-block edge copies no title or body at all. internal/sync, a later
-- step, is the only package meant to know what the content hash and the
-- cached details are used for; this migration only shapes the table and
-- its own row level security.
--
-- user_id duplicates the owning edge's own user_id rather than requiring
-- every query to join through edges to find it, the same denormalization
-- calendars and edges already apply for their own parent. The composite
-- foreign key below, not application code, is what keeps the two from
-- ever disagreeing.
CREATE TABLE event_mapping (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users (id),
    edge_id uuid NOT NULL REFERENCES edges (id),
    source_event_id text NOT NULL,
    target_event_id text NOT NULL,
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL,
    content_hash text NOT NULL,
    -- Opaque, encrypted bytes: a cached title or body, encrypted at rest
    -- with the same key and the same mechanism connections.credentials
    -- uses; see that migration's own comment. Nullable: a busy-block
    -- edge, per docs/concepts/calbridge.md's detail-level presets, caches
    -- no title or body at all.
    encrypted_details bytea,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT event_mapping_source_event_id_not_empty CHECK (source_event_id <> ''),
    CONSTRAINT event_mapping_target_event_id_not_empty CHECK (target_event_id <> ''),
    CONSTRAINT event_mapping_content_hash_not_empty CHECK (content_hash <> ''),
    -- Composite, the same technique calendars_user_id_connection_id_fkey
    -- and calendar_grants_owner_user_id_calendar_id_fkey use: ties
    -- user_id to the actual owner of the edge this mapping belongs to.
    CONSTRAINT event_mapping_user_id_edge_id_fkey FOREIGN KEY (user_id, edge_id) REFERENCES edges (user_id, id)
);

-- One mapping row per source event per edge: re-detecting the same
-- source change must update the existing row, not duplicate it.
CREATE UNIQUE INDEX event_mapping_edge_id_source_event_id_idx ON event_mapping (edge_id, source_event_id);

ALTER TABLE event_mapping ENABLE ROW LEVEL SECURITY;
ALTER TABLE event_mapping FORCE ROW LEVEL SECURITY;

-- Same mechanism as every earlier user-scoped table in this schema; see
-- identities' own comment for the full reasoning.
CREATE POLICY event_mapping_user_isolation ON event_mapping
    USING (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid)
    WITH CHECK (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid);

-- migrate:down

DROP TABLE event_mapping;
