# syntax=docker/dockerfile:1

# ---------- builder ----------
FROM golang:1.27.1-alpine AS builder

# GOCACHE matches the build-cache mount below.
ENV CGO_ENABLED=0 GOOS=linux GOTOOLCHAIN=local GOCACHE=/root/.cache/go-build

WORKDIR /src

COPY go.mod go.sum ./

RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

# seed is built in its own stage below, so release targets never pay for it.
# -tags nodynamic: gen2brain/webp otherwise dlopens libwebp via purego, which
# makes the binary dynamically linked even with CGO_ENABLED=0 and it fails
# on distroless/static with "exec /api: no such file or directory".
ARG GOFLAGS="-trimpath -tags=nodynamic"
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -ldflags="-s -w" -o /out/api         ./cmd/app && \
    go build -ldflags="-s -w" -o /out/worker      ./cmd/worker && \
    go build -ldflags="-s -w" -o /out/migrate     ./cmd/migrate

# ---------- builder-seed (dev only) ----------
# Separate stage: BuildKit skips it for --target api/worker/migrate.
FROM builder AS builder-seed

# ARG scope ends with its stage; redeclare for this one.
ARG GOFLAGS="-trimpath -tags=nodynamic"
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -ldflags="-s -w" -o /out/seed ./cmd/seed

# ---------- runtime: seed (dev only, never pushed) ----------
# One-shot demo data loader; run once against a dev database.
FROM gcr.io/distroless/static-debian12:nonroot AS seed

COPY --from=builder-seed /out/seed /seed

ENTRYPOINT ["/seed"]

# ---------- runtime: migrate (one-shot release job) ----------
# Runs before api/worker on every deploy, then exits.
# The first admin comes from ADMIN_USERNAME/ADMIN_EMAIL/ADMIN_PASSWORD on the
# api (app.New -> EnsureAdmin); cmd/createadmin is a local break-glass tool
# (go run ./cmd/createadmin) and is not shipped in any image.
FROM gcr.io/distroless/static-debian12:nonroot AS migrate

COPY --from=builder /out/migrate /migrate

ENTRYPOINT ["/migrate"]

# ---------- runtime: worker ----------
# Needs METRICS_ADDR set: -healthcheck probes /healthz on that listener.
FROM gcr.io/distroless/static-debian12:nonroot AS worker

COPY --from=builder /out/worker /worker

HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=3 \
    CMD ["/worker", "-healthcheck"]

ENTRYPOINT ["/worker"]

# ---------- runtime: api (last stage = default target) ----------
# Port comes from PORT at run time; nothing is baked into the image.
FROM gcr.io/distroless/static-debian12:nonroot AS api

COPY --from=builder /out/api /api

HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=3 \
    CMD ["/api", "-healthcheck"]

ENTRYPOINT ["/api"]
