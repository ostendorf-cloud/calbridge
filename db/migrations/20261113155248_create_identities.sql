-- migrate:up

-- identities is a provider identity used to sign in: Google, Microsoft or
-- Apple through govue's oidc, asking for identity scopes only. It is
-- deliberately a separate table from connections (the next migration),
-- which holds a provider account connected as a calendar, with its own
-- credentials and its own scopes: signing in with a provider and
-- connecting that provider's calendar are two different consent steps,
-- per docs/concepts/calbridge.md, and neither table carries a foreign key
-- or any other reference to the other. A provider identity used to sign
-- in must never be resolved through a connection someone else connected
-- as a calendar, and a connection must never be resolved through an
-- identity: the collision creates a separate account instead, which is
-- only possible when there is nothing here one query could follow from
-- one table into the other.
CREATE TABLE identities (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users (id),
    provider text NOT NULL CHECK (provider IN ('google', 'microsoft', 'apple')),
    -- The identity provider's stable subject claim, never the email or
    -- username, both of which can be reassigned to somebody else.
    subject text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT identities_subject_not_empty CHECK (subject <> ''),
    CONSTRAINT identities_user_id_id_unique UNIQUE (user_id, id)
);

-- Deliberately global, not scoped by user_id, the same way solobill's
-- invites_token_hash_idx is deliberately global rather than per-tenant: a
-- given provider subject must resolve to at most one calbridge user,
-- full stop, so two different users racing to claim the same Google
-- subject must collide here rather than silently both succeeding. A
-- unique index is enforced by Postgres independently of row level
-- security (uniqueness checking a candidate row against every existing
-- row is exactly the kind of query row level security exists to
-- restrict, and a uniqueness violation on a row owned by someone else
-- confirms its existence, not its content), so this already-accepted
-- cost is the same shape as solobill's own token_hash uniqueness, not a
-- new exception for this table.
CREATE UNIQUE INDEX identities_provider_subject_idx ON identities (provider, subject);

-- Same reasoning as solobill's accounts and invites: ENABLE alone exempts
-- the table's owning role, so FORCE is what actually binds the role that
-- runs migrations and, unless charts/calbridge's migrationJob.databaseUser
-- is set to a second role, the role the application connects as too.
ALTER TABLE identities ENABLE ROW LEVEL SECURITY;
ALTER TABLE identities FORCE ROW LEVEL SECURITY;

-- The user is read back from a transaction-local setting, set once per
-- transaction by postgres.Store.WithUser via
-- set_config('app.user_id', $1, true), the same mechanism and the same
-- NULLIF handling solobill's WithTenant uses: see that migration's own
-- comment on accounts for the full reasoning on why NULLIF is needed and
-- why an unscoped transaction, fresh connection or reused, sees zero rows
-- rather than every row or a cast error.
CREATE POLICY identities_user_isolation ON identities
    USING (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid)
    WITH CHECK (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid);

-- migrate:down

DROP TABLE identities;
