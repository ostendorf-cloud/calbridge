# calbridge

Selective calendar mirroring across providers, one detail level per edge.

## What it does

calbridge mirrors events between Google Calendar, Microsoft 365 and iCloud. A
sync edge copies one source calendar into one target calendar in one
direction, at a detail level the edge fixes: a full copy, title and time
only, or a plain busy block. That lets a work colleague see a blocked slot
while a private calendar keeps the real appointment. See
`docs/concepts/calbridge.md` for the full design: problem, goals, non goals,
architecture and the delivery order.

`cmd/calbridge` serves liveness, readiness and metrics on an administrative
listener, and on the public one local accounts with TOTP, sign in with
Google, Microsoft or Apple, and the sync graph API the Vue Flow canvas in
`web/` is a view of; that SPA is embedded into the binary at build time.
`db/migrations` and `internal/storage` hold the database foundation: users,
identities, connections, calendars, edges, calendar grants and the event
mapping, with row level security enforced by a database role that is
`NOSUPERUSER NOBYPASSRLS`. `charts/calbridge` installs all of it, and
alongside it `cmd/calbridge-landing`, the public landing page on
`calbridge.de`: its own static build, own container and own deployment, so
a correction to its privacy policy never redeploys the application on
`app.calbridge.de`. See `AGENTS.md` for what each package does and what is
still ahead.

## Install

```sh
task setup
```

Installs the pinned toolchain through `mise`.

## Running locally

Copy `.env.example` to `.env` and fill in the generated values it describes, then:

```sh
docker compose up --build
```

Builds and starts `calbridge` and a Postgres with `db/migrations` already applied, reachable
at `http://localhost:8080`, and the landing server, reachable at `http://localhost:8090`,
each linking to the other.

## Development

```sh
task check
```

Runs every check this repository has: Go format, vet and lint, a check that
the committed sqlc output still matches `db/migrations`, the Go test suite,
the `web/` and `web-landing/` packages' ESLint, Prettier, TypeScript and
Vitest checks and SPA builds, a check that `go.mod` and `web/package.json`
pin the same govue tag, markdown lint, YAML lint, a GitHub Actions workflow
lint, a secret scan, the Go build, both container image builds (`Dockerfile`
and `Dockerfile.landing`), Helm chart lint and schema validation, and the
Helm chart's own `helm-unittest` suite.

`task test:integration` runs the row level security isolation test against a
live Postgres and is deliberately not part of `check`, which runs with no
network and no port binding; the CI `integration` job runs it against a real
Postgres service container. `task --list` shows the individual targets.

Releases will be driven by
[release-me](https://github.com/ostendorf-cloud/release-me) once a first
release is worth cutting: Conventional Commits decide the version, and
merging the release pull request it maintains cuts the release.
`CHANGELOG.md` is generated, never hand edited.

## License

Elastic License 2.0, see [LICENSE](LICENSE).
