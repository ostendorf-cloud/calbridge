# db

`db/migrations/` holds the dbmate migrations.

- Tables: users, identities, connections, calendars, edges, calendar grants and the event
  mapping table.
- Row level security policies keep one user's rows apart from another's.
- The application database role is `NOSUPERUSER NOBYPASSRLS`, so the policies bind it.
- `SECURITY DEFINER` lookups are revoked from `PUBLIC` here. The explicit grant to the
  application role decides who may call them. See `internal/storage/AGENTS.md`.
- `sqlc.yaml` points sqlc's schema at `db/migrations` itself, parsed statically with no
  database connection.
- So `task generate:check` runs with no network and no port binding.
