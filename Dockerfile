# syntax=docker/dockerfile:1

# Build on the runner's native platform and cross-compile: pure Go, no CGO, no QEMU.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
ARG TARGETOS TARGETARCH
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/artifax ./cmd/artifax

# Data dir owned by the runtime user, so fresh named volumes are writable.
RUN mkdir -p /out/data

FROM gcr.io/distroless/static-debian12
LABEL org.opencontainers.image.title="artifax" \
      org.opencontainers.image.description="Self-hosted sharing for artifacts uploaded by agents" \
      org.opencontainers.image.source="https://github.com/LeonardoIz/artifax" \
      org.opencontainers.image.licenses="MIT"

COPY --from=build /out/artifax /usr/local/bin/artifax
COPY --from=build --chown=1000:1000 /out/data /data

ENV ARTIFAX_LISTEN=:8080 \
    ARTIFAX_DATA_DIR=/data
EXPOSE 8080
VOLUME ["/data"]
# Common host uid/gid, so bind mounts owned by the main host user work as-is.
# Override with `user:` in compose if your data directory belongs to someone else.
USER 1000:1000

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/usr/local/bin/artifax", "healthcheck"]

ENTRYPOINT ["/usr/local/bin/artifax"]
