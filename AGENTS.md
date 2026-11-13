# calbridge

Selective calendar mirroring across providers. The design lives in
`docs/concepts/calbridge.md`.

The global baseline already applies here and is not repeated below.

## Layout

`docs/concepts/calbridge.md` is the agreed design; the delivery order its
"Delivery order" section lists is the sequence the issues that follow
implement in steps. The paths below marked "will" record the agreed layout
for the issues still ahead, so later work lands in the place already agreed
on; every other path already exists.

- `cmd/calbridge` wires the service: `internal/config` loads and validates
  the environment, `main.go` builds `internal/httpapi`'s handler and runs it
  through govue's `httpserver` with graceful shutdown. A later issue adds
  the database connection, the provider registry and the sync engine to
  this wiring.
- `internal/config` loads calbridge's configuration from the environment
  once, at startup, through govue's `envconf`. It validates only server and
  logging settings today; a later issue's secret follows `envconf`'s
  `_FILE`-suffix convention once one exists.
- `internal/httpapi` builds the HTTP handler: liveness, readiness and
  metrics today. A later issue adds the calendar, edge and grant API, the
  webhook endpoint Google and Microsoft Graph push change notifications to,
  and the SPA fallback that serves the embedded Vue build.
- `charts/calbridge/` is the Helm chart for the real deployment, with no
  Postgres subchart, the shape `charts/solobill` uses. It exposes the
  service through either an `Ingress` or a Gateway API `HTTPRoute`, chosen
  in values, neither enabled by default.
- `scripts/validate-chart.sh` renders `charts/calbridge` for its default
  values and every documented non-default combination, shared by
  `task lint:helm` and `task validate:kubeconform` so CI and a local run
  check the exact same combinations.
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
- `web/` will hold the Vue SPA on govue: a login view, and the Vue Flow
  canvas where calendars are nodes grouped by account, an edge is drawn by
  dragging one onto another, and the detail level is edited on the edge.

## Verify before committing

```sh
task check
```

`task --list` shows the individual targets. `task check` runs every check
this repository has: Go format, vet and lint, the Go test suite, markdown
lint, YAML lint, a GitHub Actions workflow lint, a secret scan, the Go
build, the container image build, Helm chart lint and schema validation,
and the Helm chart's own `helm-unittest` suite. A later issue that adds
Vue or database tooling adds its own `lint:*`, `test:*` or `validate`
target and extends `check` with it, rather than folding its check into an
existing target.
