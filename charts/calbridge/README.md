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

## Database

This chart ships with no Postgres subchart: a database, once this application needs one, is a
connection this chart takes as configuration, reachable from the cluster, not a dependency this
chart installs or manages itself. No release of this chart configures or requires one yet;
`cmd/calbridge` reads only `LISTEN_ADDR` and `LOG_LEVEL` today, see `internal/config`.

## Values

| Key | Description | Default |
| --- | --- | --- |
| `replicaCount` | Number of pod replicas. | `1` |
| `image.repository` | Container image repository. | `ghcr.io/ostendorf-cloud/calbridge` |
| `image.tag` | Image tag. Defaults to the chart's `appVersion`. | `""` |
| `image.pullPolicy` | Image pull policy. | `IfNotPresent` |
| `serviceAccount.create` | Create a ServiceAccount for the Deployment. | `true` |
| `service.type` | Kubernetes Service type. | `ClusterIP` |
| `service.port` | Service, container and probe port. | `8080` |
| `ingress.enabled` | Expose the service through an `Ingress`. | `false` |
| `httproute.enabled` | Expose the service through a Gateway API `HTTPRoute`. | `false` |
| `httproute.parentRefs` | Gateways this `HTTPRoute` attaches to. | `[{name: ""}]` |
| `httproute.hostnames` | Hostnames this `HTTPRoute` matches. | `[]` |
| `config.listenAddr` | Address `cmd/calbridge` listens on. | `:8080` |
| `config.logLevel` | Structured log level. | `info` |

See `values.yaml` for the full list, including resources, probes and pod security settings.

## Uninstalling

```sh
helm uninstall calbridge
```

Removes every object this chart created. Nothing is left behind: this chart holds no persistent
volume and no `Secret` of its own.
