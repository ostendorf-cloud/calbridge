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
  the environment, `main.go` opens the database pool, builds the sign-in
  providers `internal/config` describes, builds `internal/httpapi`'s
  handler and runs it through govue's `httpserver` with graceful
  shutdown. A later issue adds the provider registry and the sync engine
  to this wiring.
- `internal/config` loads calbridge's configuration from the environment
  once, at startup, through govue's `envconf`. The database connection,
  the encryption key and each sign-in provider's client secret follow
  `envconf`'s `SecretOrFile`/`RequiredSecretOrFile` `_FILE`-suffix
  convention; the rest is server, logging, session, login rate limit and
  provider sign-in configuration.
- `internal/httpapi` builds the HTTP handler: liveness, readiness and
  metrics on the admin listener, and on the public one signup, login,
  logout, `GET` and `DELETE /api/v1/me`, the sign-in provider list and a
  pair of routes per configured provider, plus the SPA fallback that
  serves the embedded Vue build. Every account decision belongs to
  `internal/auth`; this package decodes a request, calls that package and
  turns the answer into JSON or a session cookie. A later issue adds the
  calendar, edge and grant API and the webhook endpoint Google and
  Microsoft Graph push change notifications to.
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
- `internal/provider` defines the one connector interface every provider
  implements: list calendars, read changes since a token, create and delete
  a calendar it owns, and create, update and delete an event it owns. A
  connector registers itself into a registry under a provider id and
  declares its capabilities (push notifications, native recurrence rules,
  calendar creation, attendees, floating times), so adding Google Calendar,
  Microsoft Graph or a CalDAV profile is a new package and one line of
  wiring, never an edit to a switch statement in the engine. Connectors
  speak one normalised event type and never leak a provider's own model;
  everything provider-shaped stays inside its package. A connector also
  declares whether it writes one overridden occurrence and whether it
  rewrites content on write, and answers what a write turns an event into
  where it does.
- `internal/provider/caldav` is the generic CalDAV connector, driven by
  one profile per server with iCloud as the first. It is the only
  connector so far.
- `internal/providertest` holds the in-memory connector every consumer of
  `internal/provider` tests against. It is not a test file: both
  `internal/provider`'s own tests and `internal/sync`'s import it, because
  a fake written once per consumer is one contract read twice, and the
  readings drift.
- `internal/sync` turns a source change into target operations by applying
  an edge's field mask, and is the only package meant to know what a detail
  level means. It owns the mapping table (edge id, source event id, target
  event id, start, end, content hash), recurrence expansion for the case
  where source and target disagree on a rule, and the marker that stops
  calbridge's own writes from being copied onward as source changes. The
  full reconcile reads both calendars from the zero sync token, so it
  depends on no stored position and no delivered notification, and the
  teardown of an edge, including one whose grant was revoked, removes every
  event it wrote, the edge row included. A mirrored event edited by hand
  in the target is overwritten on the next reconcile, and a revoked grant
  takes the edge with it; see `docs/adr/`.
- `docs/adr/` holds the decisions worth remembering, in the MADR shape, one
  per numbered file.
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
  above this package never holds or passes ciphertext. The two lookups
  that have to run before any user id is known, a local account by email
  and a sign-in identity by `(provider, subject)`, go through their own
  `SECURITY DEFINER` function instead of through a widened policy; both
  are revoked from `PUBLIC` in `db/migrations`, so the explicit grant to
  the application role is what decides who may call them.
- `internal/storage/storagetest` is an in-memory implementation of the
  same repository interfaces, for a test of a package above the storage
  layer. It imitates the scoping row level security enforces, so a
  repository method called outside a `WithUser` transaction fails there
  too.
- `internal/auth` holds login: local accounts with a password hashed
  with Argon2id and a TOTP second factor, plus sign in with Google,
  Microsoft and Apple through govue's `oidc` package, asking for identity
  scopes only. calbridge owns identity and depends on no external
  identity provider. Both paths produce one `Identity`, so nothing above
  branches on login method. A provider identity used to sign in is never
  matched against a provider account another user connected as a
  calendar; that collision creates a separate account, and the rule is
  structural: the `Store` interface this package defines exposes no
  connection repository, so no code here can reach one. How many
  Argon2id derivations may run at once is capped here too, against the
  chart's own memory limit; `internal/depsync` fails if the two stop
  agreeing.
- `cmd/calbridge-mcp` will be the MCP server, its own binary and its own
  deployment, letting an agent read the sync topology and create or change
  edges and grants. It may not create, change or delete a provider
  connection, and it calls the same authorisation rules as the web API
  rather than carrying its own copy.
- `web/` holds the Vue SPA on govue: a login shell and an authenticated
  shell that prove the session and CSRF contract end to end, with
  German and English i18n and a theme generated from `web/src/theme/seed.json`
  through govue's own generator. `LoginView` carries sign-in with both
  factors, sign-up with its TOTP enrolment, and a button per configured
  sign-in provider, with Apple marked as granting identity only. The Vue
  Flow canvas where calendars are nodes grouped by account, an edge is
  drawn by dragging one onto another, and the detail level is edited on
  the edge, is a later issue's work.

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
