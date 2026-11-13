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

Only the concept, this repository scaffold and its checks exist today. No Go
code, no Vue application, no database migration and no Helm chart have been
written yet.

## Install

```sh
task setup
```

Installs the pinned toolchain through `mise`.

## Development

```sh
task check
```

Runs every check this repository has today: markdown lint, YAML lint, a
GitHub Actions workflow lint and a secret scan. `task --list` shows the
individual targets.

Releases will be driven by
[release-me](https://github.com/ostendorf-cloud/release-me) once a first
release is worth cutting: Conventional Commits decide the version, and
merging the release pull request it maintains cuts the release.
`CHANGELOG.md` is generated, never hand edited.

## License

Elastic License 2.0, see [LICENSE](LICENSE).
