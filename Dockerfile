# syntax=docker/dockerfile:1
# The bare `golang:1.25` tag is the minor-tracking tag: it floats to the latest
# 1.25.x patch published on Docker Hub. Deliberately not pinned to an exact
# patch (e.g. golang:1.25.11) so builds pick up Go security/patch releases
# automatically. go.mod's `go 1.25.0` line is the minimum the code requires.
FROM golang:1.25 AS build

ARG VERSION=dev
ARG TARGETOS=linux
ARG TARGETARCH=amd64

ENV CGO_ENABLED=0 \
    GOOS=${TARGETOS} \
    GOARCH=${TARGETARCH}

WORKDIR /src

# Download modules first so this layer is reused while go.mod/go.sum are
# unchanged. The cache mount keeps already-fetched modules around even when
# the layer must re-run.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

RUN mkdir -p /out /tmp/nodexia-data

# Cache mounts persist the module cache and the Go build cache (compiled
# packages) across builds. The first build is cold, but every later build —
# including installer re-runs/updates — only recompiles changed packages,
# turning a ~100s rebuild into a few seconds.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/nodexia ./cmd/nodexia

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app

COPY --from=build --chown=nonroot:nonroot /out/nodexia /app/nodexia
COPY --from=build --chown=nonroot:nonroot /tmp/nodexia-data /var/lib/nodexia

LABEL org.opencontainers.image.title="Nodexia" \
      org.opencontainers.image.description="Lightweight SSH-first control plane for servers and infrastructure nodes." \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.source="https://github.com/Ho3einK84/Nodexia"

EXPOSE 8080

VOLUME ["/var/lib/nodexia"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 CMD ["/app/nodexia", "healthcheck"]

ENTRYPOINT ["/app/nodexia"]
