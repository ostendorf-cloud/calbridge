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
  and `check` binds no port. The `integration` CI job runs it against a real `postgres`
  service container.
- `check` needs the network beyond a warm cache: the shared `lint:agents-md` include loads from
  github.com, `validate:kubeconform` fetches schemas from raw.githubusercontent.com, and a
  cold cache also downloads pnpm packages, Go modules and base images.
- Two build tags exist: `builtfrontend` and `integration`. `.golangci.yaml` lists both under
  `run.build-tags`, so `task lint:golangci` lints every tagged file. Without the tag a linter
  never loads the file and reports `0 issues`.
  - A new tag goes into that list in the same commit that adds the first file behind it.
  - `golangci-lint fmt` reads every file, so the format check needs no list.
  - `task lint:vet` runs a plain `go vet ./...`. The `govet` linter inside `lint:golangci` is
    what covers the tagged files.
- A later issue adds its own `lint:*`, `test:*` or `validate` target and extends `check` with
  it, rather than folding its check into an existing target.

## Checks that cannot run in the sandbox

A red result from one of these says nothing about the change. CI runs every one of them, so
report them as not run locally, never as passed, and never report a full `task check` run.

- `task` itself fails in the Claude sandbox:
  `mkdir ~/Library/Caches/task/task-git-repos: operation not permitted`. The remote include
  cache is not writable. Run the commands under the targets instead: `go build ./...`,
  `go vet ./...`, `go test -race -count=1 ./...`, `golangci-lint run ./...`,
  `golangci-lint fmt --diff`, and `pnpm run <script>` inside `web/` and `web-landing/`.
- The `web:*` and `landing-web:*` targets fail without a writable `XDG_RUNTIME_DIR`. pnpm cannot
  take its store lock and stops with `ERR_PNPM_STORE_DIR_OPEN_OPERATION_LOCK`. Set it for the
  command. The sandbox rejects a bare `mktemp -d`, so root the directory in `$TMPDIR`:

  ```sh
  XDG_RUNTIME_DIR="$(mktemp -d "$TMPDIR/xdg.XXXXXX")" pnpm install --frozen-lockfile
  ```

  - A parallel session sets its own directory. Six overlapping installs on one shared
    directory all passed in a test, so this is a precaution, not a measured need.
  - `go build`, `go vet`, `go test` and `golangci-lint run` need both frontends built first.
- `task build:image` and `task landing:build:image` need a container runtime. No `docker`
  works in the sandbox, and `podman` cannot connect.
  - In the sandbox, `task` fails before any target runs. Without `XDG_RUNTIME_DIR`, the `web:*`
    targets and the targets that depend on `web:build` fail before `build:image` is reached.
  - `check` runs `build:image` before the targets that follow it in `Taskfile.yaml`. A local
    `check` therefore stops there. That is read from the target order, not run.
  - Run these on their own: `landing:build`, `test:go`, `test:builtfrontend`,
    `check:govue-version`, `lint:helm` and `test:helm`.
- `task validate:kubeconform` fails where a filtering proxy sits in the way. kubeconform cannot
  download the Kubernetes schemas and reports
  `tls: failed to verify certificate: x509: OSStatus -26276` for every resource. CI passes it.
  The failure is neither a reason to hold a change nor a pass.
- `task test:integration` needs a reachable Postgres in `DATABASE_URL`. Without it, and with
  `CI` unset, every Postgres test skips and `go test` still prints `ok`. Anything resting on it
  is unverified locally. With `CI` set, the same tests fail instead of skipping.
