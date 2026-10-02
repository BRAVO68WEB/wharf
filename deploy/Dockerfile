# syntax=docker/dockerfile:1
# better-docker-registry production image.
#
# Build from the repository root (the build context must contain go.mod and
# the source tree):
#
#   docker build -t bdr .
#
# The final stage is distroless/static: no shell, no package manager, no OS
# utilities — just the registry binary. The HEALTHCHECK must run real
# `curl -f http://localhost:5000/healthz`, and distroless ships no curl, so
# the runtime-deps stage copies Alpine's curl together with its shared-
# library closure (including the musl loader) into the final image. Everything
# else stays out.

# ---- stage 1: build the static registry binary ------------------------
FROM golang:1.26-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0
# Module downloads are cached in their own layer: source edits do not
# re-download dependencies.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /out/registry ./cmd/registry

# ---- stage 2: curl + musl closure + data directory for the final image -
FROM alpine:3.22 AS runtime-deps
RUN apk add --no-cache curl \
    && mkdir -p /runtime/usr/bin /runtime/etc/registry /runtime/var/lib/registry \
    && cp /usr/bin/curl /runtime/usr/bin/curl \
    && for lib in $(ldd /usr/bin/curl | awk '{print $3} /^\// {print $1}'); do \
           case "$lib" in \
               /*) mkdir -p "/runtime$(dirname "$lib")"; cp "$lib" "/runtime$lib" ;; \
           esac; \
       done \
    && chown -R 65532:65532 /runtime/var/lib/registry

# ---- stage 3: minimal runtime -----------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

# Registry binary at /registry; curl and its libraries at their standard
# paths so exec-form HEALTHCHECK lookup works.
COPY --from=build /out/registry /registry
COPY --from=runtime-deps /runtime/ /

# Default configuration (override with --config or a mounted file). The
# /var/lib/registry data directory comes from the runtime-deps stage already
# owned by uid 65532, so named volumes inherit writable ownership.
COPY deploy/config.yml /etc/registry/config.yml

# Never run as root. distroless ":nonroot" defines user nonroot (uid 65532).
USER nonroot:nonroot

EXPOSE 5000

# Liveness against /healthz. Exec form: distroless has no /bin/sh.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["curl", "-f", "http://localhost:5000/healthz"]

ENTRYPOINT ["/registry"]
CMD ["--config", "/etc/registry/config.yml"]
