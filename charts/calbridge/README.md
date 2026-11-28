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
  --set config.databaseUser=calbridge_app \
  --set migrationJob.databaseUser=calbridge_migrator
```

The chart has no usable default for `existingSecret.name`, `config.databaseUrl`,
`config.databaseUser` or `migrationJob.databaseUser`: the render fails an install or upgrade
that leaves any of them empty, with a message naming the missing key. The default values
otherwise install calbridge with neither an `Ingress` nor a Gateway API `HTTPRoute`: nothing
routes external traffic to it until one is enabled.

This chart is also published as an OCI artifact, versioned in lock-step with every release, once
a publish workflow for it exists; none does yet, so for now install or `helm pull` it by path
from a checkout of this repository, as above.

## Migrations run as a separate role

This chart's own migration Job (below) always authenticates as `migrationJob.databaseUser`, a
role distinct from `config.databaseUser` (the render fails otherwise, naming both), configured
alongside its own password key, `existingSecret.migrationDatabasePasswordKey` (falling back to
`existingSecret.databasePasswordKey` when left unset). This mirrors
`docker-compose.yaml`'s own two-role split: a bootstrap superuser used only to apply
`db/migrations`, and a separate, restricted role the application actually connects as, see that
file's own comment for the full reasoning.

The reason the two must differ: `migrationJob.databaseUser` runs `db/migrations` and therefore
OWNS every table this chart's schema creates. A correctly-provisioned `config.databaseUser`
(`NOSUPERUSER NOBYPASSRLS`) is still fully bound by `FORCE ROW LEVEL SECURITY` for every
ordinary application query, since ownership is irrelevant to `FORCE` and only a superuser or a
`BYPASSRLS` role bypasses it. But a role that both migrates and serves the application would,
precisely because it owns the tables, also be able to run
`ALTER TABLE ... NO FORCE ROW LEVEL SECURITY` or drop the policy outright: PostgreSQL's own
`ALTER TABLE` reference documents ownership, not `FORCE`, as what that command checks. Letting
the two collapse into one role would accept a weaker boundary than
`docker-compose.yaml`'s own two-role split: a compromised or buggy application process could
not just read another user's rows through a live connection, but could also switch the
protection off for every user at once, by virtue of owning the tables. This chart therefore
never renders with the two equal.

`config.databaseUser` needs its privileges granted by `migrationJob.databaseUser`, which owns
the tables. This chart cannot do that itself: it does not own the database and runs no step
after the migration Job that could, so provisioning them is the cluster operator's
responsibility. The list is not left to the operator to work out, though, because a privilege
granted here that is not granted elsewhere makes
`internal/storage/postgres`'s own row level security tests prove less than they claim. It is
exactly what `docker-compose.yaml`'s `grant-app-role` service applies, and
`internal/depsync`'s `TestTheApplicationRoleAndTheTestRoleHoldTheSameGrants` is what keeps the
three copies from drifting apart. It reads the block below as one of its three sources, with
`<config.databaseUser>` standing in for the role name, and compares the privileges each list
grants per object rather than the statement text, so reordering or rewrapping a statement here
is not drift and adding a privilege is:

```sql
GRANT USAGE ON SCHEMA public TO <config.databaseUser>;
GRANT SELECT, INSERT, UPDATE, DELETE ON users TO <config.databaseUser>;
GRANT SELECT, INSERT, DELETE ON identities, calendars, calendar_grants TO <config.databaseUser>;
GRANT SELECT, INSERT, UPDATE, DELETE ON connections, edges, event_mapping TO <config.databaseUser>;
GRANT EXECUTE ON FUNCTION users_find_by_email(text) TO <config.databaseUser>;
GRANT EXECUTE ON FUNCTION identities_find_by_provider_subject(text, text) TO <config.databaseUser>;
GRANT EXECUTE ON FUNCTION grant_counterparties() TO <config.databaseUser>;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO <config.databaseUser>;
```

Three things about that list are deliberate rather than incidental:

- Each table gets exactly the privileges its own repository in `internal/storage/postgres`
  uses, and no more. `users` gets no `DELETE` because nothing deletes a user row other than
  account deletion, which does hold it. `calendars`, `identities` and `calendar_grants` get no
  `UPDATE` because none of them has an update query; for `calendars` that withheld privilege is
  a guarantee an integration test asserts directly, so granting it here without revisiting that
  test would weaken a check rather than widen a permission.
- The three `EXECUTE` grants are the only way `config.databaseUser` reads a row it does not
  own: a local account by email (also what resolves the address a grant names), a sign-in
  identity by `(provider, subject)`, and the addresses on the other side of a grant. Each
  function is revoked from `PUBLIC` in `db/migrations`, so these grants are what actually decide
  who may call them.
- `ALTER DEFAULT PRIVILEGES` covers a table a later migration adds, and only one added after it
  runs, by the role that ran it. It cannot retroactively cover the tables above, which is why
  each of them is still named explicitly. It is compared like every other statement here, and
  the integration test's own role carries the same rule: without that, the first table a
  migration adds would arrive with `UPDATE` in a deployment and without it in the test, and an
  assertion about what the role may not do would hold there for a reason no deployment has.

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

## Provider sign-in

`internal/auth` adds sign in with Google, Microsoft and Apple, each independent and off by
default: a provider is offered only once its `config.auth.<provider>.clientId` is set.

`config.authBaseUrl` is the public base URL of this deployment, for example
`https://calbridge.example.com`. `internal/auth` builds every provider's redirect URL from it as
`<AUTH_BASE_URL>/api/v1/auth/providers/<provider>/callback`, and this must match the redirect URL
registered for the application at that provider's own console; a mismatch there is how the
provider rejects the sign-in attempt, not a failure this chart can catch at render time.

Each configured provider's client secret is read from the same existing `Secret` as
`DATABASE_PASSWORD` and `ENCRYPTION_KEY` above, key by key, exactly like those two: this chart
never offers a values-driven way to set a client secret, so none ever appears in `values.yaml`
or in `helm template`/`helm install --set` output.

| Env var | `values.yaml` key | Default key in the Secret |
| --- | --- | --- |
| `AUTH_GOOGLE_CLIENT_SECRET` | `existingSecret.googleClientSecretKey` | `AUTH_GOOGLE_CLIENT_SECRET` |
| `AUTH_MICROSOFT_CLIENT_SECRET` | `existingSecret.microsoftClientSecretKey` | `AUTH_MICROSOFT_CLIENT_SECRET` |
| `AUTH_APPLE_CLIENT_SECRET` | `existingSecret.appleClientSecretKey` | `AUTH_APPLE_CLIENT_SECRET` |

`existingSecret.name` is required on every install regardless, since `DATABASE_PASSWORD` and
`ENCRYPTION_KEY` are read from it too, so a configured provider always has a Secret to read its
client secret from.

`config.auth.<provider>.issuer` stays empty unless a deployment needs a non-default issuer:
`internal/auth` falls back to its own documented default issuer for each provider when this is
left unset. A deployment restricted to one Microsoft Entra ID tenant is the case this is meant
for; the default is Microsoft's multi-tenant `common` authority.

### What address a provider account gets

calbridge uses a provider's email claim as the account's address only when the ID token marks
that claim verified. Otherwise the account gets a placeholder under `sign-in.calbridge.invalid`,
derived from the provider's subject, which is a name that can never resolve (RFC 2606).

For two of the three providers that is every account, not an exception:

| Provider | Address the account gets |
| --- | --- |
| Google | the verified email claim |
| Microsoft | always a placeholder: Entra ID emits no `email_verified` claim in either token version, and Microsoft's own ID token claims reference says to never use `email` for authorization or to save data for a user |
| Apple | always a placeholder: calbridge never requests the email scope, since that would move Apple's callback to a POST |

That placeholder is the address `GET /api/v1/me` returns and the UI shows, and nothing in this
release can change it. Believing an unverified claim instead would let a tenant administrator
name somebody else's address, so this is a deliberate trade and not an oversight. If your users
need to see their real address, Google sign-in and local accounts are the two paths that give
them one today; letting a person confirm and correct their own address is tracked separately.

Apple sign-in grants identity and nothing else: a person who signs in with Apple gets an
account, and no calendar is connected. An iCloud calendar is connected separately over CalDAV
with an Apple ID and an app-specific password, because Apple has no calendar API equivalent to
Google Calendar or Microsoft Graph.

Apple's client secret is not a fixed string like the other two. It is an ES256 JWT the operator
mints from a key downloaded once from the Apple Developer portal, and Apple refuses one whose
`exp` is more than six months out. Nothing in this chart or in calbridge can renew it, and
nothing warns before it lapses: Apple sign-in simply starts failing. Treat the Secret key it
lives under as something with an expiry date, re-mint it on a schedule, and roll the Secret
before the old one runs out. Google and Microsoft client secrets have their own expiry policies
set per application at those providers, which is worth checking when the credential is created.

## Closing registration

Registration is open by default. Until `config.registrationClosed` is `true`, an instance
reachable from the internet is open to anyone who can reach it:

- Anyone can create a local account through `POST /api/v1/auth/signup`.
- Anyone with an account at a configured provider can create one by signing in with it.

Both paths create an account on first use, so both sit behind one switch. Set
`config.registrationClosed` to `true` once the accounts that should exist do.

- A closed instance answers both paths with `403` and the code `permission_denied`.
- No account is created.
- An account that already exists keeps signing in, by password or by provider.
- The login screen stops offering signup.

A closed instance has no invite flow, so a new person can only be added by opening it again
for as long as it takes them to sign up.

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

## The rate limits are per client only with trusted proxies configured

`cmd/calbridge` rate limits two sets of routes by the client address its
limiters resolve, through govue's `clientip` package: the ones a request
reaches with no session (`POST /api/v1/auth/signup`,
`POST /api/v1/auth/login` and the two provider sign-in routes) and
`POST /api/v1/grants`, which answers whether an account exists for an
address. The two have separate budgets, so sharing calendars cannot use
up what a login needs. `config.trustedProxyCIDRs` is empty by default,
which is the secure choice: with nothing configured, the resolver trusts
no proxy and keys every request on its TCP peer.

`templates/ingress.yaml` and `templates/httproute.yaml` both route every
request through the chosen `Ingress` controller or Gateway data plane
before it reaches this Service, so with `config.trustedProxyCIDRs` left
empty, the TCP peer calbridge sees is that controller or data plane for
every request alike, not the client. Each budget then ends up shared by
every client instead of kept one per client, and one client exhausting
one trips that limit for everyone else too.

Set `config.trustedProxyCIDRs` to the address or CIDR range the ingress
controller's or Gateway data plane's pods send traffic from, as a comma
separated list, so the resolver trusts the `X-Forwarded-For` entry that
component appends and recovers the real client address instead. The right
value depends on the ingress controller or Gateway implementation in the
cluster; consult its own documentation for the pod network or source
address its data plane uses to reach a Service, and verify the resolved
address changes per client rather than staying fixed before relying on it.

## The public landing server

This chart renders two workloads, not one: the application
(`templates/deployment.yaml` and friends) and, alongside it, the public landing
server on `templates/landing-deployment.yaml`, `templates/landing-service.yaml`,
`templates/landing-serviceaccount.yaml`, `templates/landing-ingress.yaml` and
`templates/landing-httproute.yaml`. It is a separate static build
(`cmd/calbridge-landing`, see `internal/landingapi` and `internal/landingconfig`)
with its own container image, its own `Deployment` and its own route, so a
correction to the privacy policy's text is a new image for this workload alone,
never a redeployment of the application.

Both workloads always render; only each one's route is optional, and each one's
`ingress.enabled`/`httproute.enabled` pair is independent of the other's.
`landing.ingress.enabled` and `landing.httproute.enabled` are mutually
exclusive, exactly like the application's own pair above, and neither is
enabled by default.

`landing.config.appUrl` is the application's own public base URL, for example
`https://app.calbridge.example.com`. `internal/landingapi` renders it into
every page the landing server serves in place of its own placeholder, so the
same built image serves any deployment's own application host; it has no
usable default and the render fails without it, the same shape
`config.databaseUrl` and `existingSecret.name` already take above.

`landing.config.operator` is the real-world operator the imprint, the privacy
statement and the terms name. The chart renders it as the `OPERATOR_*`
environment variables `internal/landingconfig` reads. The values are not
secret, so they sit in the pod spec and not in a Secret. The repository holds
no operator data and the chart has no default for it.

- `name`, `street`, `postalCode`, `city` and `email` are required. The landing
  server refuses to start without them, and the render fails first.
- `country`, `phone`, `representative`, `register`, `vatId`,
  `contentResponsible` and `dataProtection` are optional. An empty value renders
  no variable, and its line stays out of the pages.
- Every field is a string of at most 300 characters. Set `postalCode` with
  `--set-string` or quote it in a values file. `--set` reads `01067` as the
  number 1067, and the schema rejects a number.
- The landing server also rejects a placeholder such as `[Postal address]` or
  `TODO`, a control character and an email address without an `@` and a dot in
  the domain part.

```sh
helm install calbridge charts/calbridge \
  --set landing.config.operator.name="Example Operator" \
  --set landing.config.operator.street="Example Street 1" \
  --set-string landing.config.operator.postalCode=01067 \
  --set landing.config.operator.city="Example City" \
  --set landing.config.operator.email=legal@calbridge.example.com
```

`landing.config.listenAddr` must encode the same port as `landing.service.port`,
and `landing.config.adminListenAddr` the same port as `landing.service.adminPort`,
for the same reason the application's own "Metrics and probes live on their own
port" section gives; the render fails with a readable message otherwise.

```sh
# Ingress
helm install calbridge charts/calbridge \
  --set landing.ingress.enabled=true \
  --set landing.ingress.className=nginx \
  --set landing.ingress.hosts[0].host=calbridge.example.com \
  --set landing.ingress.hosts[0].paths[0].path=/ \
  --set landing.ingress.hosts[0].paths[0].pathType=Prefix

# Gateway API HTTPRoute
helm install calbridge charts/calbridge \
  --set landing.httproute.enabled=true \
  --set landing.httproute.parentRefs[0].name=my-gateway \
  --set landing.httproute.parentRefs[0].namespace=gateway-system \
  --set landing.httproute.hostnames[0]=calbridge.example.com
```

The landing server carries no session, no CSRF protector and no database
connection at all: see `internal/landingapi`'s own package doc for why.

The link runs the other way too: `config.publicSiteUrl`, the landing page's
own base URL, for example `https://calbridge.example.com`. `internal/httpapi`
renders it into the application's login screen and signed-in shell as the
"back to the public site" link. Empty by default, which renders no such
link, the correct shape for a deployment that has not published a landing
page, or installs the application standalone (`landing.config.appUrl` and
`config.publicSiteUrl` are two independent, unrelated settings: setting one
does not imply or require the other).

## Database

This chart ships with no Postgres subchart: the database is a connection this chart takes as
configuration (`config.databaseUrl`, `config.databaseUser`, `existingSecret`), reachable from
the cluster, not a dependency this chart installs or manages itself. See "Migrations run as a
separate role", "Migrations" and "Secrets" above for what it requires, and `internal/config`
for the full list `cmd/calbridge` reads today.

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
| `config.databaseUser` | `DATABASE_USER`. Required; no usable default. See "Migrations run as a separate role" above. | `""` |
| `existingSecret.name` | Name of the existing Secret carrying every key below. Required; no usable default. | `""` |
| `existingSecret.databasePasswordKey` | Key for `DATABASE_PASSWORD` in that Secret. | `DATABASE_PASSWORD` |
| `existingSecret.encryptionKeyKey` | Key for `ENCRYPTION_KEY` in that Secret. | `ENCRYPTION_KEY` |
| `existingSecret.migrationDatabasePasswordKey` | Key for `migrationJob.databaseUser`'s password. Falls back to `databasePasswordKey` when empty. | `""` |
| `migrationJob.enabled` | Run `db/migrations` as a pre-install,pre-upgrade hook Job. See "Migrations" above. | `true` |
| `migrationJob.databaseUser` | A separate role that applies `db/migrations`, distinct from `config.databaseUser`. Required; no usable default. See "Migrations run as a separate role" above. | `""` |
| `migrationJob.image.repository` / `migrationJob.image.tag` / `migrationJob.image.digest` | The dbmate image the migration Job runs. | `ghcr.io/amacneil/dbmate` / `2.38.1` / pinned, see `values.yaml` |
| `config.trustedProxyCIDRs` | `TRUSTED_PROXY_CIDRS`, a comma separated list of CIDRs (or bare addresses). Empty keeps the rate limiters keyed on the TCP peer; see "The rate limits are per client only with trusted proxies configured" above. | `""` |
| `config.authBaseUrl` | `AUTH_BASE_URL`, the public base URL provider redirect URLs are built from. See "Provider sign-in" above. | `""` |
| `config.publicSiteUrl` | `PUBLIC_SITE_URL`, the public landing page's own base URL. Rendered into the login screen and the signed-in shell as the link back to the public site. Empty renders no such link. See "The public landing server" above. | `""` |
| `config.registrationClosed` | `REGISTRATION_CLOSED`. `true` refuses every new account, by password signup and by first provider sign-in. Existing accounts keep signing in. See "Closing registration" above. | `false` |
| `landing.config.appUrl` | `APP_URL`, the application's own public base URL the landing server links every call to action at. Required; no usable default. See "The public landing server" above. | `""` |
| `landing.config.operator.name` / `.street` / `.postalCode` / `.city` / `.email` | `OPERATOR_NAME`, `OPERATOR_STREET`, `OPERATOR_POSTAL_CODE`, `OPERATOR_CITY` and `OPERATOR_EMAIL`, the operator the imprint, privacy statement and terms name. Required; no usable default. See "The public landing server" above. | `""` |
| `landing.config.operator.country` / `.phone` / `.representative` / `.register` / `.vatId` / `.contentResponsible` / `.dataProtection` | `OPERATOR_COUNTRY`, `OPERATOR_PHONE`, `OPERATOR_REPRESENTATIVE`, `OPERATOR_REGISTER`, `OPERATOR_VAT_ID`, `OPERATOR_CONTENT_RESPONSIBLE` and `OPERATOR_DATA_PROTECTION`. Optional. Empty renders no variable and leaves the line out of the pages. | `""` |
| `config.auth.google.clientId` / `config.auth.microsoft.clientId` / `config.auth.apple.clientId` | `AUTH_GOOGLE_CLIENT_ID` / `AUTH_MICROSOFT_CLIENT_ID` / `AUTH_APPLE_CLIENT_ID`. Empty means that provider's sign-in is not offered. See "Provider sign-in" above. | `""` |
| `config.auth.google.issuer` / `config.auth.microsoft.issuer` / `config.auth.apple.issuer` | `AUTH_GOOGLE_ISSUER` / `AUTH_MICROSOFT_ISSUER` / `AUTH_APPLE_ISSUER`. Empty falls back to `internal/auth`'s own documented default issuer. See "Provider sign-in" above. | `""` |
| `existingSecret.googleClientSecretKey` / `existingSecret.microsoftClientSecretKey` / `existingSecret.appleClientSecretKey` | Keys for each provider's client secret in that Secret. See "Provider sign-in" above. | `AUTH_GOOGLE_CLIENT_SECRET` / `AUTH_MICROSOFT_CLIENT_SECRET` / `AUTH_APPLE_CLIENT_SECRET` |

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
