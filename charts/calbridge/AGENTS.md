# charts/calbridge

`charts/calbridge/` is the Helm chart for the real deployment. It has no Postgres subchart, the
shape `charts/solobill` uses.

- It renders two workloads, not one.
- The application: `templates/deployment.yaml` and friends.
- The public landing server, under the `landing` values key: `templates/landing-deployment.yaml`
  and friends.
- Each workload exposes its own service through either an `Ingress` or a Gateway API
  `HTTPRoute`, chosen in values.
- Neither is enabled by default on either workload.
- Each pair is independent of the other's.

## Validation

- `scripts/validate-chart.sh` renders the chart for its default values and every documented
  non-default combination, application and landing server alike.
- `task lint:helm` and `task validate:kubeconform` share it, so CI and a local run check the
  exact same combinations.
- A new values combination goes into that script.
- `internal/depsync` fails if the chart's memory limit and the Argon2id concurrency cap in
  `internal/auth` stop agreeing.
