-- User isolation here is two independent layers, matching
-- docs/concepts/calbridge.md: every query below names user_id in its own
-- WHERE clause (or, for CreateIdentity, as a column value), AND row
-- level security (see db/migrations) enforces the same scope below these
-- queries regardless of what they ask for. The transaction these queries
-- run in has already been scoped to one user via
-- set_config('app.user_id', ...) by postgres.Store.WithUser;
-- current_setting(..., true)::uuid reads that same value back.

-- name: CreateIdentity :one
INSERT INTO identities (user_id, provider, subject)
VALUES ($1, $2, $3)
RETURNING id, user_id, provider, subject, created_at;

-- name: GetIdentityByID :one
SELECT id, user_id, provider, subject, created_at
FROM identities
WHERE id = $1 AND user_id = NULLIF(current_setting('app.user_id', true), '')::uuid;

-- name: ListIdentities :many
SELECT id, user_id, provider, subject, created_at
FROM identities
WHERE user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
ORDER BY created_at;

-- name: DeleteIdentity :execrows
DELETE FROM identities
WHERE id = $1 AND user_id = NULLIF(current_setting('app.user_id', true), '')::uuid;
