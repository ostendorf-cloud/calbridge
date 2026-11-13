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

`cmd/calbridge` serves liveness, readiness and metrics today, and
`charts/calbridge` installs it; see `AGENTS.md` for what each package does
and what is still ahead. No Vue application, no database migration and no
provider connector have been written yet.

## Install

```sh
task setup
```

Installs the pinned toolchain through `mise`.

## Running locally

```sh
docker compose up --build
```

Builds and starts `calbridge`, reachable at `http://localhost:8080`.

## Development

```sh
task check
```

Runs every check this repository has: Go format, vet and lint, the Go test
suite, markdown lint, YAML lint, a GitHub Actions workflow lint, a secret
scan, the Go build, Helm chart lint and schema validation, and the Helm
chart's own `helm-unittest` suite. `task --list` shows the individual
targets.

Releases will be driven by
[release-me](https://github.com/ostendorf-cloud/release-me) once a first
release is worth cutting: Conventional Commits decide the version, and
merging the release pull request it maintains cuts the release.
`CHANGELOG.md` is generated, never hand edited.

## License

Elastic License 2.0, see [LICENSE](LICENSE).
