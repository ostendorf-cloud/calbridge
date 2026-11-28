-- migrate:up

-- The scheduled reconcile has to know whose edges to run, and every
-- table that answers that is policied on user_id: a connection with no
-- user scope sees no row at all. A background pass has no user to scope
-- to until it knows one, the same chicken and egg problem
-- users_find_by_email solves for login.
--
-- This is the same bypass with the same reasoning. It returns the ids of
-- the users who own at least one edge and nothing else about them: no
-- email, no calendar, no edge. The pass then scopes itself to each id in
-- turn and reads the rest under row level security as that user.
--
-- search_path is pinned so a caller cannot influence what "edges"
-- resolves to. EXECUTE is revoked from PUBLIC because PostgreSQL grants
-- it there by default, and the explicit grant to the application role
-- (docker-compose.yaml's "grant-app-role" service, charts/calbridge's
-- README for a cluster operator) is what decides who may call it.
--
-- It returns one array rather than a set of rows because sqlc, which
-- parses these files without a database, resolves a scalar result and
-- cannot resolve the column list of a function returning a table.
CREATE FUNCTION edge_owners() RETURNS uuid[]
LANGUAGE sql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
    SELECT COALESCE(array_agg(DISTINCT edges.user_id), ARRAY[]::uuid[])
    FROM edges;
$$;

REVOKE EXECUTE ON FUNCTION edge_owners() FROM PUBLIC;

-- migrate:down

DROP FUNCTION edge_owners();
