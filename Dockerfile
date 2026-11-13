# syntax=docker/dockerfile:1

# Go build stage: compiles a fully static binary. CGO_ENABLED=0 is safe
# here: jackc/pgx/v5, the Postgres driver internal/storage/postgres uses,
# is pure Go with no cgo dependency of its own, and nothing else in this
# binary links one either, so there is no libc dependency this build is
# dropping by going static. Pinned to 1.27.0-bookworm, the exact patch
# mise.toml pins go to: see mise.toml's own comment for why
# golangci-lint 2.15.0 specifically needs that exact patch, a constraint
# this image is kept aligned with even though golangci-lint itself never
# runs here.
FROM golang:1.27.0-bookworm AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ cmd/
COPY internal/ internal/

RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/calbridge ./cmd/calbridge

# Runtime stage: gcr.io/distroless/static-debian12 carries a minimal root
# filesystem, ca-certificates and tzdata, and nothing else - no shell, no
# package manager, no busybox. That trust store matters once a later
# issue's provider connectors make outbound TLS connections of their own
# (Google, Microsoft Graph, iCloud CalDAV) and is kept from the start
# rather than added once needed. This mirrors solobill's own Dockerfile:
# same base image, same ":nonroot" tag, same reasoning.
#
# Pinned by tag, not digest: resolving and recording a digest needs a
# registry pull, which this sandbox could not make (no network egress to
# a container registry). ":nonroot" is itself the distroless project's
# own maintained, documented tag, not a floating "latest" - the same
# choice solobill's own Dockerfile already made.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/calbridge /usr/local/bin/calbridge

# Matches LISTEN_ADDR's default (":8080", internal/config.Load). Override
# LISTEN_ADDR and this EXPOSE together if a deployment needs another port.
EXPOSE 8080

# Numeric, not "nonroot:nonroot": the same uid/gid the base image's own
# nonroot user already resolves to, kept explicit anyway, matching
# solobill's own Dockerfile.
USER 65532:65532

# No HEALTHCHECK: this image has no shell and no curl/wget to exec one
# with. docker-compose.yaml does not depend on this image's own health as
# a result.
ENTRYPOINT ["/usr/local/bin/calbridge"]
