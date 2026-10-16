# syntax=docker/dockerfile:1

# Declared before the first FROM so every FROM line can use them.
ARG GO_VERSION=1.27.1
ARG DISTROLESS_TAG=nonroot

# ---------- builder ----------
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS builder

# GOCACHE matches the build-cache mount below.
ENV CGO_ENABLED=0 GOTOOLCHAIN=local GOCACHE=/root/.cache/go-build

WORKDIR /src

COPY go.mod go.sum ./

RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

# -tags nodynamic: gen2brain/webp otherwise dlopens libwebp via purego, which
# makes the binary dynamically linked even with CGO_ENABLED=0 and it fails
# on distroless/static with "exec /api: no such file or directory".
# The tag is hard-coded in the build command so --build-arg GOFLAGS cannot drop it.
ARG GOFLAGS="-trimpath"
# Set by buildx from --platform; defaults cover a plain docker build.
ARG TARGETOS=linux
ARG TARGETARCH
# Changes on every commit, so declared as late as possible to keep the cache.
ARG VERSION=dev
ARG COMMIT=unknown
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    LDFLAGS="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" && \
    export GOOS=${TARGETOS} GOARCH=${TARGETARCH} && \
    go build -tags=nodynamic -ldflags="$LDFLAGS" -o /out/api         ./cmd/app && \
    go build -tags=nodynamic -ldflags="$LDFLAGS" -o /out/worker      ./cmd/worker && \
    go build -tags=nodynamic -ldflags="$LDFLAGS" -o /out/migrate     ./cmd/migrate

# ---------- runtime: migrate (one-shot release job) ----------
# Runs before api/worker on every deploy, then exits.
# The first admin comes from ADMIN_USERNAME/ADMIN_EMAIL/ADMIN_PASSWORD on the
# api (app.New -> EnsureAdmin); cmd/createadmin is a local break-glass tool
# (go run ./cmd/createadmin) and is not shipped in any image.
FROM gcr.io/distroless/static-debian12:${DISTROLESS_TAG} AS migrate

ARG VERSION=dev COMMIT=unknown
LABEL org.opencontainers.image.version=$VERSION org.opencontainers.image.revision=$COMMIT

COPY --from=builder /out/migrate /migrate

ENTRYPOINT ["/migrate"]

# ---------- runtime: worker ----------
# Needs METRICS_ADDR set: -healthcheck probes /healthz on that listener.
FROM gcr.io/distroless/static-debian12:${DISTROLESS_TAG} AS worker

ARG VERSION=dev COMMIT=unknown
LABEL org.opencontainers.image.version=$VERSION org.opencontainers.image.revision=$COMMIT

COPY --from=builder /out/worker /worker

HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=3 \
    CMD ["/worker", "-healthcheck"]

ENTRYPOINT ["/worker"]

# ---------- runtime: api (last stage = default target) ----------
# Port comes from PORT at run time; nothing is baked into the image.
FROM gcr.io/distroless/static-debian12:${DISTROLESS_TAG} AS api

ARG VERSION=dev COMMIT=unknown
LABEL org.opencontainers.image.version=$VERSION org.opencontainers.image.revision=$COMMIT

COPY --from=builder /out/api /api

# Background jobs run in the separate worker image; .env can still override.
ENV WORKER=false

HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=3 \
    CMD ["/api", "-healthcheck"]

ENTRYPOINT ["/api"]
