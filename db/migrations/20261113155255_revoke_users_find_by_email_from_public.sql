-- migrate:up

-- users_find_by_email is executable by PUBLIC. PostgreSQL grants EXECUTE
-- on a new function to PUBLIC by default, and the migration that created
-- it (20261113155247_create_users.sql) revoked nothing, so every role in
-- this database can call a SECURITY DEFINER function that bypasses row
-- level security and returns whole users rows, password_hash and
-- totp_secret included.
--
-- This is a new migration rather than an edit to that one: dbmate
-- records a migration by its own name once it has run, so a database
-- that already applied it would never see the change.
--
-- The function itself is not the problem and is left exactly as it is:
-- its search_path is pinned and its body reads users alone. What this
-- changes is who may call it. The application role is granted EXECUTE
-- explicitly wherever its other grants are made (docker-compose.yaml's
-- "grant-app-role" service; a cluster operator's own grant for
-- charts/calbridge), which is what that grant now actually decides.
REVOKE EXECUTE ON FUNCTION users_find_by_email(text) FROM PUBLIC;

-- migrate:down

GRANT EXECUTE ON FUNCTION users_find_by_email(text) TO PUBLIC;
