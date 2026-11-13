# calbridge

Installs [calbridge](https://github.com/ostendorf-cloud/calbridge) on a Kubernetes cluster.

## Prerequisites

- Kubernetes 1.27 or newer.
- A PostgreSQL database reachable from the cluster, with `db/migrations` either already
  applied or applicable by this chart's own migration Job (see "Migrations" below).
- **The database role this chart connects as must be `NOSUPERUSER` and `NOBYPASSRLS`.**
  `db/migrations` enables `FORCE ROW LEVEL SECURITY` on every table that references `users`:
  that is the actual per-user isolation guarantee, not application code. `FORCE` binds the
  policy to the table's own owning role too, but a Postgres superuser, or any role carrying
  the `BYPASSRLS` attribute, is exempt from row level security regardless of `FORCE`. If the
  role named by `config.databaseUser` has either property, every row-level-security policy
  this application depends on provides no actual protection, silently. Provisioning that role
  correctly is the cluster operator's responsibility: this chart has no way to enforce it from
  here, and never creates the role itself.
- A `Secret` already in the target namespace, created by your own secret manager operator,
  carrying the required database password and encryption key (see "Secrets" below). This
  chart never creates that `Secret` itself.
- A Gateway API controller already installed in the cluster, if `httproute.enabled` is used
  instead of `ingress.enabled`. This chart never installs the Gateway API CRDs or a Gateway
  itself.

## Installing

```sh
helm install calbridge charts/calbridge \
  --set existingSecret.name=calbridge-secrets \
  --set config.databaseUrl=postgres://postgres.example.svc:5432/calbridge?sslmode=require \
  --set config.databaseUser=calbridge_app
```

The chart has no usable default for `existingSecret.name`, `config.databaseUrl` or
`config.databaseUser`: the render fails an install or upgrade that leaves any of them empty,
with a message naming the missing key. The default values otherwise install calbridge with
neither an `Ingress` nor a Gateway API `HTTPRoute`: nothing routes external traffic to it until
one is enabled.

This chart is also published as an OCI artifact, versioned in lock-step with every release, once
a publish workflow for it exists; none does yet, so for now install or `helm pull` it by path
from a checkout of this repository, as above.

## One role, or two

Unlike `docker-compose.yaml`'s own local development setup, which brings its own throwaway
Postgres and therefore has to script a second, restricted role itself (a bootstrap superuser
used only to apply `db/migrations`, and a separate `NOSUPERUSER NOBYPASSRLS` role the
application actually connects as, see that file's own comment for the full reasoning), this
chart's Postgres already runs in the cluster it installs into.

By default, this chart's own migration Job (below) and its Deployment authenticate as the
exact same role, configured once by `config.databaseUser` and
`existingSecret.databasePasswordKey`. A correctly-provisioned `NOSUPERUSER NOBYPASSRLS` role
is still fully bound by `FORCE ROW LEVEL SECURITY` for every ordinary application query:
ownership is irrelevant to `FORCE`, only a superuser or a `BYPASSRLS` role bypasses it. But
that same role, because it is the one that ran `db/migrations` and therefore OWNS every table
this chart's schema creates, can also run `ALTER TABLE ... NO FORCE ROW LEVEL SECURITY` or
drop the policy outright: PostgreSQL's own `ALTER TABLE` reference documents ownership, not
`FORCE`, as what that command checks. A single role that both migrates and serves the
application therefore accepts a weaker boundary than `docker-compose.yaml`'s own two-role
split: a compromised or buggy application process could not just read another user's rows
through a live connection, but could also switch the protection off for every user at once, by
virtue of owning the tables.

Set `migrationJob.databaseUser` (and, if its password lives under a different key in the same
Secret, `existingSecret.migrationDatabasePasswordKey`) to a separate role the application
itself never authenticates as, to get the stronger, two-role boundary instead, mirroring
`docker-compose.yaml`'s own bootstrap-superuser-for-migrations-only split. Left empty, the
default, this chart keeps the single-role behaviour above.

## Migrations

A pre-install,pre-upgrade Helm hook Job applies `db/migrations` with
[dbmate](https://github.com/amacneil/dbmate) before this chart's own Deployment is created or
updated, an upgrade included, so a new image's pods never start against an old schema. The
migration files themselves are embedded into the chart as a `ConfigMap`, through a
`charts/calbridge/migrations` symlink to the repository's own `db/migrations` (not a
hand-copied duplicate: `helm.Files.Glob` only ever reads inside a chart's own directory, so the
symlink is what keeps this chart's migrations always matching what `internal/storage` actually
needs). `dbmate up` is idempotent (it tracks what it already applied in its own
`schema_migrations` table), so re-running it on every upgrade against an already-migrated
database is safe.

Disable it with `migrationJob.enabled: false` for a deployment that applies `db/migrations`
some other way; see "Values" below.

The migration Job assembles its own connection URL from `config.databaseUrl`,
`migrationJob.databaseUser` (falling back to `config.databaseUser` when unset; see "One role,
or two" above) and the existing Secret's password with plain POSIX shell parameter expansion,
not a URL encoder: the password must therefore contain no character that needs
percent-encoding in a URL (no `@`, `/`, `:`, `%`, whitespace, ...). This organisation's own
secret-generation convention (`openssl rand -hex ...`, see `docker-compose.yaml`) already
produces hex-only passwords, which have none; generate the password the same way.

## Secrets

Every secret-shaped setting is read from one existing `Secret`, named by
`existingSecret.name`, key by key. This chart never offers a values-driven way to create its
own `Secret` from plaintext values: the `Secret` itself comes from your own secret manager
operator (External Secrets, Vault, SOPS, or similar), and this chart only ever references it
by name.

| Env var | `values.yaml` key | Default key in the Secret | Required |
| --- | --- | --- | --- |
| `DATABASE_PASSWORD` | `existingSecret.databasePasswordKey` | `DATABASE_PASSWORD` | Yes |
| `ENCRYPTION_KEY` | `existingSecret.encryptionKeyKey` | `ENCRYPTION_KEY` | Yes |

Neither key has a fallback: a `Secret` missing `databasePasswordKey` or `encryptionKeyKey`
fails the pod at the Kubernetes level (`CreateContainerConfigError`), not silently with an
empty value. `DATABASE_PASSWORD` must be free of characters that need percent-encoding in a
URL; see "Migrations" above. `ENCRYPTION_KEY` is hex encoded, exactly 32 bytes decoded
(`internal/config`'s own `encryptionKeyBytes`, matching `internal/storage/postgres`'s AES-256
key length), for example generated with `openssl rand -hex 32`: it encrypts
`connections.credentials` and `event_mapping.encrypted_details` at rest, per
`db/migrations`.

## Why `config.databaseUrl` is plain here

`config.databaseUrl` is a plain value, not read from `existingSecret`, because
`internal/config.Config`'s own `DatabaseURL` field is documented as "the connection URL without
credentials: scheme, host, port, database name and query parameters only" — `config.databaseUser`
and the secret-only `DATABASE_PASSWORD` are assembled into a real connection separately
(`internal/storage/postgres.NewPoolWithCredentials`), not carried in the URL itself.

## Exposing the service

Enable at most one of `ingress.enabled` and `httproute.enabled`. The render fails with a
readable message when both are enabled, rather than installing two routes that disagree about
who owns traffic for this release:

```sh
# Ingress
helm install calbridge charts/calbridge \
  --set ingress.enabled=true \
  --set ingress.className=nginx \
  --set ingress.hosts[0].host=calbridge.example.com \
  --set ingress.hosts[0].paths[0].path=/ \
  --set ingress.hosts[0].paths[0].pathType=Prefix

# Gateway API HTTPRoute
helm install calbridge charts/calbridge \
  --set httproute.enabled=true \
  --set httproute.parentRefs[0].name=my-gateway \
  --set httproute.parentRefs[0].namespace=gateway-system \
  --set httproute.hostnames[0]=calbridge.example.com
```

`httproute.parentRefs` must name at least one Gateway; the render fails otherwise, rather than
installing an `HTTPRoute` that nothing ever attaches.

## Metrics and probes live on their own port

`cmd/calbridge` runs two listeners, through govue's `httpserver.RunAdmin`: the public one,
`config.listenAddr`/`service.port`, and a second, administrative one,
`config.adminListenAddr`/`service.adminPort`, serving liveness, readiness and metrics. The
`Service` exposes both, each as its own named port (`http` and `admin`); `templates/ingress.yaml`
and `templates/httproute.yaml` both route only to `service.port`, never to `service.adminPort`,
so a caller reaching this application through either still cannot scrape its metrics or poll its
probes that way.

`config.listenAddr` must encode the same port as `service.port`, and `config.adminListenAddr` the
same port as `service.adminPort`: the render fails with a readable message otherwise, rather than
installing a Deployment whose probes and Service point at a port `cmd/calbridge` never listens
on.

The readiness probe's `periodSeconds` and `failureThreshold` set how long the Service and the
route keep sending new connections to a pod that has started shutting down; `config.drainDelay`
must stay above their product, so govue's graceful shutdown has already failed that probe for a
full period before it closes the listener. `terminationGracePeriodSeconds` in turn stays above
`config.drainDelay` plus however long an in-flight request may take to finish, so the kubelet
never kills the container before that shutdown sequence gets to finish on its own. The three
defaults already satisfy both orderings; changing one means checking the other two.

## Database

This chart ships with no Postgres subchart: the database is a connection this chart takes as
configuration (`config.databaseUrl`, `config.databaseUser`, `existingSecret`), reachable from
the cluster, not a dependency this chart installs or manages itself. See "One role, or two",
"Migrations" and "Secrets" above for what it requires, and `internal/config` for the full list
`cmd/calbridge` reads today.

## Values

| Key | Description | Default |
| --- | --- | --- |
| `replicaCount` | Number of pod replicas. | `1` |
| `image.repository` | Container image repository. | `ghcr.io/ostendorf-cloud/calbridge` |
| `image.tag` | Image tag. Defaults to the chart's `appVersion`. | `""` |
| `image.pullPolicy` | Image pull policy. | `IfNotPresent` |
| `serviceAccount.create` | Create a ServiceAccount for the Deployment. | `true` |
| `podLabels` | Extra labels on the pod template. Must not set `app.kubernetes.io/name` or `app.kubernetes.io/instance`; the render fails otherwise, since either would collide with this chart's own immutable selector label. | `{}` |
| `service.type` | Kubernetes Service type. | `ClusterIP` |
| `service.port` | Service and public container port. Keep in sync with `config.listenAddr`. | `8080` |
| `service.adminPort` | Administrative container port (liveness, readiness, metrics). Keep in sync with `config.adminListenAddr`. Exposed on the Service as its own named port; the `Ingress` and the `HTTPRoute` still never route to it. See "Metrics and probes live on their own port" above. | `8081` |
| `ingress.enabled` | Expose the service through an `Ingress`. | `false` |
| `httproute.enabled` | Expose the service through a Gateway API `HTTPRoute`. | `false` |
| `httproute.parentRefs` | Gateways this `HTTPRoute` attaches to. | `[{name: ""}]` |
| `httproute.hostnames` | Hostnames this `HTTPRoute` matches. | `[]` |
| `readinessProbe.periodSeconds` / `readinessProbe.failureThreshold` | Readiness probe timing. Their product is the drain window; see "Metrics and probes live on their own port" above. | `2` / `1` |
| `terminationGracePeriodSeconds` | How long Kubernetes waits before killing the container. Must stay above `config.drainDelay`. | `30` |
| `config.listenAddr` | `LISTEN_ADDR`. Keep in sync with `service.port`. | `:8080` |
| `config.adminListenAddr` | `ADMIN_LISTEN_ADDR`. Keep in sync with `service.adminPort`. | `:8081` |
| `config.drainDelay` | `DRAIN_DELAY`, a Go duration. Must stay above `readinessProbe.periodSeconds` times `readinessProbe.failureThreshold`. | `5s` |
| `config.logLevel` | Structured log level. | `info` |
| `config.databaseUrl` | `DATABASE_URL`, without credentials. Required; no usable default. The role `config.databaseUser` names MUST be `NOSUPERUSER` and `NOBYPASSRLS`. | `""` |
| `config.databaseUser` | `DATABASE_USER`. Required; no usable default. See "One role, or two" above. | `""` |
| `existingSecret.name` | Name of the existing Secret carrying every key below. Required; no usable default. | `""` |
| `existingSecret.databasePasswordKey` | Key for `DATABASE_PASSWORD` in that Secret. | `DATABASE_PASSWORD` |
| `existingSecret.encryptionKeyKey` | Key for `ENCRYPTION_KEY` in that Secret. | `ENCRYPTION_KEY` |
| `existingSecret.migrationDatabasePasswordKey` | Key for `migrationJob.databaseUser`'s password. Falls back to `databasePasswordKey` when empty. | `""` |
| `migrationJob.enabled` | Run `db/migrations` as a pre-install,pre-upgrade hook Job. See "Migrations" above. | `true` |
| `migrationJob.databaseUser` | A separate role that applies `db/migrations`, distinct from `config.databaseUser`. See "One role, or two" above. | `""` |
| `migrationJob.image.repository` / `migrationJob.image.tag` | The dbmate image the migration Job runs. | `ghcr.io/amacneil/dbmate` / `2.38.1` |

See `values.yaml` for the full list, including resources, startup and liveness probes, and pod
security settings.

## Upgrading

A plain `helm upgrade` is safe for any change to this chart's values: there is no persistent
state in the chart itself and no migration step runs today. No release has shipped a breaking
change to a value's name or shape yet; one that does will be noted here with the version it
changed in.

## Uninstalling

```sh
helm uninstall calbridge
```

Removes every object this chart created. Nothing is left behind: this chart holds no persistent
volume and no `Secret` of its own.
