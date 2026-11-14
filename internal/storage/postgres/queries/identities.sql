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

-- GetIdentityByProviderSubject is the one query here that runs against
-- the pool directly rather than inside a WithUser transaction, the same
-- exception and the same mechanism GetUserByEmail already is: resolving
-- a provider sign-in to a calbridge user happens before any user id is
-- known at all. identities_find_by_provider_subject is a SECURITY
-- DEFINER function reading identities and nothing else; see
-- db/migrations for why it may not grow a join to connections.
-- name: GetIdentityByProviderSubject :one
SELECT *
FROM identities_find_by_provider_subject(sqlc.arg(provider), sqlc.arg(subject));
