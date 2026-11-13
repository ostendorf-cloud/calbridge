# calbridge

Installs [calbridge](https://github.com/ostendorf-cloud/calbridge) on a Kubernetes cluster.

## Prerequisites

- Kubernetes 1.27 or newer.
- A Gateway API controller already installed in the cluster, if `httproute.enabled` is used
  instead of `ingress.enabled`. This chart never installs the Gateway API CRDs or a Gateway
  itself.

## Installing

```sh
helm install calbridge charts/calbridge
```

The default values install calbridge with neither an `Ingress` nor a Gateway API `HTTPRoute`:
nothing routes external traffic to it until one is enabled.

This chart is also published as an OCI artifact, versioned in lock-step with every release, once
a publish workflow for it exists; none does yet, so for now install or `helm pull` it by path
from a checkout of this repository, as above.

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

This chart ships with no Postgres subchart: a database, once this application needs one, is a
connection this chart takes as configuration, reachable from the cluster, not a dependency this
chart installs or manages itself. No release of this chart configures or requires one yet; see
`internal/config` for the full list `cmd/calbridge` reads today.

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
