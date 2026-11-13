# calbridge

Selective calendar mirroring across providers. The design lives in
`docs/concepts/calbridge.md`.

The global baseline already applies here and is not repeated below.

## Layout

None of the paths below exist yet. This records the agreed layout from the
concept doc, for the scaffold and the issues that build it, so later work
lands in the place already agreed on.

- `cmd/calbridge` will wire the service: `internal/config` loads and
  validates the environment, `main.go` builds the database connection, the
  provider registry, the sync engine and `internal/httpapi`'s handler, then
  runs it through govue's `httpserver` with graceful shutdown.
- `docs/concepts/calbridge.md` is the agreed design; the delivery order its
  "Delivery order" section lists is the sequence the issues that follow
  implement in steps.
- `db/migrations/` will hold the dbmate migrations: calendars, edges, grants
  and the event mapping table, with the row level security policies that
  keep one user's edges and grants apart from another's, enforced by a
  database role that is `NOSUPERUSER NOBYPASSRLS`.
- `internal/provider` will define the one connector interface every
  provider implements: list calendars, read changes since a token, create,
  update and delete an event the connector itself owns. A connector
  registers itself into a registry under a provider id and declares its
  capabilities (push notifications, native recurrence rules, calendar
  creation, attendees, free/busy), so adding Google Calendar, Microsoft
  Graph or a CalDAV profile is a new package and one import, never an edit
  to a switch statement in the engine. Connectors speak one normalised
  event type and never leak a provider's own model; everything
  provider-shaped stays inside its package.
- `internal/sync` will turn a source change into target operations by
  applying an edge's field mask, and is the only package meant to know what
  a detail level means. It owns the mapping table (edge id, source event
  id, target event id, start, end, content hash), recurrence expansion for
  the case where source and target disagree on a rule, and the marker that
  stops a connector from reporting calbridge's own writes back as a source
  change.
- `internal/storage` will define the domain types and the repository
  interfaces the layers above call, with nothing here importing a provider
  SDK or generated sqlc code directly.
- `internal/storage/postgres` will be the only implementation, wiring
  sqlc-generated queries into the repository interfaces, the split
  solobill's `internal/storage` and `internal/storage/postgres` already
  use.
- `internal/httpapi` will build the HTTP server: health and metrics
  endpoints, the calendar, edge and grant API, the webhook endpoint Google
  and Microsoft Graph push change notifications to, and the SPA fallback
  that serves the embedded Vue build for everything else.
- `internal/auth` will hold login: local accounts with password and TOTP,
  plus sign in with Google, Microsoft and Apple through govue's `oidc`
  package. calbridge owns identity and depends on no external identity
  provider. A provider identity used to sign in is never linked to a
  provider account another user connected as a calendar; that collision
  creates a separate account.
- `cmd/calbridge-mcp` will be the MCP server, its own binary and its own
  deployment, letting an agent read the sync topology and create or change
  edges and grants. It may not create, change or delete a provider
  connection, and it calls the same authorisation rules as the web API
  rather than carrying its own copy.
- `internal/config` will load calbridge's configuration from the
  environment once, at startup, through govue's `envconf`, with every
  secret following its `_FILE`-suffix convention.
- `web/` will hold the Vue SPA on govue: a login view, and the Vue Flow
  canvas where calendars are nodes grouped by account, an edge is drawn by
  dragging one onto another, and the detail level is edited on the edge.
- `charts/calbridge/` will be the Helm chart for the real deployment, with
  no Postgres subchart, the shape `charts/solobill` already uses.

## Verify before committing

```sh
task check
```

`task --list` shows the individual targets. `task check` runs every check
this repository has today: markdown lint, YAML lint, a GitHub Actions
workflow lint and a secret scan. A later issue that adds Go, Vue, database
or Helm tooling adds its own `lint:*`, `test:*` or `validate` target and
extends `check` with it, rather than folding its check into an existing
target.
