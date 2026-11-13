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
listener, and on the public one a login shell and an authenticated shell that
prove the session and CSRF contract; the Vue SPA in `web/` is embedded into the
binary at build time. `db/migrations` and `internal/storage` hold the database
foundation: users, identities, connections, calendars, edges, calendar grants
and the event mapping, with row level security enforced by a database role that
is `NOSUPERUSER NOBYPASSRLS`. `charts/calbridge` installs all of it; see
`AGENTS.md` for what each package does and what is still ahead. No provider
connector has been written yet.

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
at `http://localhost:8080`.

## Development

```sh
task check
```

Runs every check this repository has: Go format, vet and lint, a check that
the committed sqlc output still matches `db/migrations`, the Go test suite,
the `web/` package's ESLint, Prettier, TypeScript and Vitest checks, the
`web/` SPA build, a check that `go.mod` and `web/package.json` pin the same
govue tag, markdown lint, YAML lint, a GitHub Actions workflow lint, a secret
scan, the Go build, the container image build, Helm chart lint and schema
validation, and the Helm chart's own `helm-unittest` suite.

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

Apache License 2.0, see [LICENSE](LICENSE).
