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
  the provider registry and the sync engine, and opens the database
  connection `internal/config` now reads, to this wiring.
- `internal/config` loads calbridge's configuration from the environment
  once, at startup, through govue's `envconf`. The database connection and
  the encryption key follow `envconf`'s `SecretOrFile`/`RequiredSecretOrFile`
  `_FILE`-suffix convention; every other setting is server and logging
  configuration.
- `internal/httpapi` builds the HTTP handler: liveness, readiness and
  metrics on the admin listener, and on the public one a session and
  CSRF contract shell (`POST /api/v1/auth/login`, `GET /api/v1/me`,
  `POST /api/v1/auth/logout`) plus the SPA fallback that serves the
  embedded Vue build. The login handler starts a session for whatever
  email it is given, with no account to check it against yet; issue #5
  replaces it with real local accounts and provider sign-in, built on
  `internal/auth`, without changing the session or CSRF wiring around
  it. A later issue adds the calendar, edge and grant API and the
  webhook endpoint Google and Microsoft Graph push change notifications
  to.
- `charts/calbridge/` is the Helm chart for the real deployment, with no
  Postgres subchart, the shape `charts/solobill` uses. It exposes the
  service through either an `Ingress` or a Gateway API `HTTPRoute`, chosen
  in values, neither enabled by default.
- `scripts/validate-chart.sh` renders `charts/calbridge` for its default
  values and every documented non-default combination, shared by
  `task lint:helm` and `task validate:kubeconform` so CI and a local run
  check the exact same combinations.
- `db/migrations/` holds the dbmate migrations: users, identities,
  connections, calendars, edges, calendar grants and the event mapping
  table, with the row level security policies that keep one user's rows
  apart from another's, enforced by a database role that is
  `NOSUPERUSER NOBYPASSRLS`. `sqlc.yaml` points sqlc's schema at this
  directory itself, parsed statically with no database connection, so
  `task generate:check` runs with no network and no port binding.
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
- `internal/storage` defines the domain types and the repository
  interfaces the layers above call, with nothing here importing a provider
  SDK or generated sqlc code directly.
- `internal/storage/postgres` is the only implementation, wiring
  sqlc-generated queries (`queries/`, committed generated code in
  `sqlcgen/`) into the repository interfaces, the split solobill's
  `internal/storage` and `internal/storage/postgres` already use. Every
  user-owned table is reached only through `Store.WithUser`, which scopes
  a transaction to one user via a transaction-local Postgres setting; row
  level security, not this package, is what actually enforces that scope.
  `crypto.go` encrypts and decrypts `connections.credentials` and
  `event_mapping.encrypted_details` at the database boundary, so a caller
  above this package never holds or passes ciphertext.
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
- `web/` holds the Vue SPA on govue: a login shell and an authenticated
  shell that prove the session and CSRF contract end to end, with
  German and English i18n and a theme generated from `web/src/theme/seed.json`
  through govue's own generator. The Vue Flow canvas where calendars
  are nodes grouped by account, an edge is drawn by dragging one onto
  another, and the detail level is edited on the edge, is a later
  issue's work; so is any login UI beyond the shell, which belongs to
  issue #5.

## Deviations

- The `Dockerfile`'s web stage installs corepack with `npm install -g`, the one
  place in this repository where a tool does not come from `mise`. Node stopped
  distributing corepack with v25, so the pinned node image carries none, and
  there is no `mise` inside that stage to resolve one. The pnpm version it then
  activates is still the exact version `mise.toml` resolves to, so the image and
  a local run use the same pnpm.

## Verify before committing

```sh
task check
```

`task --list` shows the individual targets. `task check` runs every check
this repository has: Go format, vet and lint, a check that the committed
sqlc output still matches `db/migrations`, the Go test suite, the `web/`
package's ESLint, Prettier, TypeScript and Vitest checks, the `web/` SPA
build, a check that `go.mod` and `web/package.json` pin the same govue
tag, markdown lint, YAML lint, a GitHub Actions workflow lint, a secret
scan, the Go build, the container image build, Helm chart lint and schema
validation, and the Helm chart's own `helm-unittest` suite.

`task build` and `task test:go` depend on `task web:build`: `cmd/calbridge`
embeds `web/`'s build output, and `go:embed` fails on an empty `dist/`.

`task test:integration` is deliberately not part of `check`: it needs a
reachable Postgres, and `check` runs with no network and no port binding.
The `integration` CI job runs it against a real `postgres` service
container instead. A later issue adds its own `lint:*`, `test:*` or
`validate` target and extends `check` with it, rather than folding its
check into an existing target.
