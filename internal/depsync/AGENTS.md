# internal/depsync

`internal/depsync` holds checks, not described properties. Two guard the landing split.

- `landingimports_builtfrontend_test.go` fails when `cmd/calbridge-landing`'s transitive imports
  name a calbridge package outside `internal/landingapi` and `internal/landingconfig`.
- `bundleassets_builtfrontend_test.go` fails when either build's output fetches anything off
  its own origin or names a host outside its allow list. That is the privacy policy's "no
  analytics, no third parties" claim.
- The filter and the scan, each tested on their own, need nothing built. They stay in
  `landingimports_test.go` and `bundleassets_test.go` and run in the ordinary suite.
- `licenseheader_test.go` fails when a source file or `.license-header.txt` names another SPDX
  identifier than `LICENSE` does, or names none. `addlicense` checks presence only.
- Its fixtures are small repositories under `testdata/license`. One case per way the check
  fails: a wrong header in a Go file, in a Vue file, a missing identifier, a swapped `LICENSE`,
  a stale header template and a `LICENSE` it cannot identify.
- A new license in `LICENSE` needs its title and identifier in `knownLicenses`. An unknown
  `LICENSE` fails the check instead of passing it.
- A further check fails when the chart's memory limit and the Argon2id cap in `internal/auth`
  stop agreeing.

## The builtfrontend tag

- The two checks above need both frontends built, so they sit behind the `builtfrontend` build
  tag.
- They read `cmd/calbridge/dist` and `cmd/calbridge-landing/dist`. Neither directory is
  committed.
- Run them with `task test:builtfrontend`. It depends on `web:build` and `landing-web:build`,
  so the precondition holds wherever it runs.
- The `built-frontend` CI job runs that target, not a bare `go test`.
- `go test -tags=builtfrontend ./internal/depsync/...` by hand with either `dist/` absent fails
  and names the target to run instead.
- The tag exists so that a job running `go test ./internal/depsync/...` straight after a
  checkout is not asked for a build it never made. `check:govue-version` and `integration` both
  do that.
- Neither test skips itself when the directory is missing. A guard that passes quietly checks
  nothing.
