# calbridge

Selective calendar mirroring across providers. The design lives in
`docs/concepts/calbridge.md`.

The global baseline already applies here and is not repeated below.

## Layout

`docs/concepts/calbridge.md` is the agreed design. The delivery order its "Delivery order"
section lists is the sequence the issues that follow implement in steps. Paths marked "will"
record the agreed layout for issues still ahead. Every other path already exists.

- `cmd/calbridge` wires the service. `internal/config` loads and validates the environment.
  `main.go` opens the database pool, builds the sign-in providers `internal/config` describes,
  builds `internal/httpapi`'s handler and runs it through govue's `httpserver` with graceful
  shutdown. It also builds the provider registry, the sync engine and the mirror service.
- `internal/mirror` runs the sync engine inside the service. See `internal/mirror/AGENTS.md`.
- `cmd/calbridge-landing` is the public landing server. See `cmd/calbridge-landing/AGENTS.md`.
- `cmd/calbridge-mcp` will be the MCP server. See `docs/calbridge-mcp.md`.
- `internal/config` loads the configuration from the environment once, at startup, through
  govue's `envconf`. See `internal/config/AGENTS.md`.
- `internal/httpapi` builds the HTTP handler. See `internal/httpapi/AGENTS.md`.
- `internal/provider`, `internal/provider/caldav` and `internal/providertest` hold the
  connectors. See `internal/provider/AGENTS.md`.
- `internal/sync` turns a source change into target operations. See `internal/sync/AGENTS.md`.
- `internal/storage` and `internal/storage/postgres` hold the repositories. See
  `internal/storage/AGENTS.md`.
- `internal/auth` holds login. See `internal/auth/AGENTS.md`.
- `internal/depsync` holds the cross-package guard checks. See `internal/depsync/AGENTS.md`.
- `db/migrations/` holds the dbmate migrations. See `db/AGENTS.md`.
- `charts/calbridge/` is the Helm chart and `scripts/validate-chart.sh` renders it. See
  `charts/calbridge/AGENTS.md`.
- `web/` is the Vue SPA. See `web/AGENTS.md`.
- `web-landing/` is the landing server's Vue SPA. See `web-landing/AGENTS.md`.
- `docs/adr/` holds the decisions worth remembering, in the MADR shape, one per numbered file.
- `docs/deliberate-gaps.md` lists what the code does not do yet, on purpose.
- `docs/legal-review.md` records the human review the landing pages still need.

## Deviations

- The `Dockerfile`'s web stage, and `Dockerfile.landing`'s own equivalent stage, install
  corepack with `npm install -g`, the one place in this repository where a tool does not come
  from `mise`. Node stopped distributing corepack with v25, so the pinned node image carries
  none, and there is no `mise` inside that stage to resolve one. The pnpm version it then
  activates is still the exact version `mise.toml` resolves to, so the image and a local run use
  the same pnpm.
- yamllint's line-length limit here is 140, not the default 120. A pinned action reference then
  fits on one line with its version comment. This matches most of the organisation's own
  repositories.

## Verify before committing

```sh
task check
```

`task --list` shows the individual targets.

- `task build` and `task test:go` depend on `task web:build` and `task landing-web:build`.
  `cmd/calbridge` embeds `web/`'s build output and `cmd/calbridge-landing` embeds
  `web-landing/`'s. `go:embed` fails on an empty `dist/` for either.
- `task landing:build` and `task landing:build:image` are their own targets, not folded into
  `build` and `build:image`. A correction to the landing server's own copy builds and verifies
  without touching the application's binary or image.
- `task test:builtfrontend` is the Go suite's other half, behind the `builtfrontend` build tag.
  See `internal/depsync/AGENTS.md`.
- `task test:integration` is deliberately not part of `check`. It needs a reachable Postgres,
  and `check` binds no port. `check` needs no network either, except that the shared
  `lint:agents-md` include loads from github.com on a fresh cache. The `integration` CI job
  runs it against a real `postgres` service container.
- Two build tags exist: `builtfrontend` and `integration`. `.golangci.yaml` lists both under
  `run.build-tags`, so `task lint:golangci` lints every tagged file. Without the tag a linter
  never loads the file and reports `0 issues`.
  - A new tag goes into that list in the same commit that adds the first file behind it.
  - `golangci-lint fmt` reads every file, so the format check needs no list.
  - `task lint:vet` runs a plain `go vet ./...`. The `govet` linter inside `lint:golangci` is
    what covers the tagged files.
- A later issue adds its own `lint:*`, `test:*` or `validate` target and extends `check` with
  it, rather than folding its check into an existing target.
