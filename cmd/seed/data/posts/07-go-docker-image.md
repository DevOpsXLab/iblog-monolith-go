---
title: 1 GB'dan 12 MB'gacha: Go uchun ideal Docker image
subtitle: Multi-stage build, distroless va build cache'dan to'g'ri foydalanish
author: jasur
category: Docker
tags: docker, go, containers, security
labels: Qo'llanma, Tavsiya etiladi
days_ago: 63
cover: shipping-containers
---
`FROM golang` bilan yig'ilgan image 1 GB atrofida bo'ladi: kompilyator, git, shell va yuzlab paket — barchasi prod'da keraksiz va hujum yuzasini kengaytiradi.

## Multi-stage Dockerfile

```dockerfile
# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/app

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/app"]
```

Natija: **12 MB**, shell yo'q, root yo'q.

## Nima uchun aynan shunday?

- **go.mod avval nusxalanadi** — kod o'zgarganda dependency qatlami cache'dan olinadi.
- **`--mount=type=cache`** — modul va build cache'lar image'ga kirmaydi, lekin keyingi build'larda qayta ishlatiladi.
- **`CGO_ENABLED=0`** — statik binary, libc kerak emas.
- **`-trimpath -ldflags="-s -w"`** — lokal yo'llar va debug ma'lumotlari olib tashlanadi.
- **distroless/static** — faqat CA sertifikatlar, tzdata va `/etc/passwd`.

## Healthcheck shell'siz

Distroless'da `curl` yo'q. Ilovaning o'zi healthcheck rejimini qo'llasin:

```go
if len(os.Args) > 1 && os.Args[1] == "-healthcheck" {
	resp, err := http.Get("http://127.0.0.1:8080/healthz")
	if err != nil || resp.StatusCode != 200 {
		os.Exit(1)
	}
	os.Exit(0)
}
```

```yaml
healthcheck:
  test: ["CMD", "/app", "-healthcheck"]
```

## Debug kerak bo'lsa

Prod konteynerga shell qo'shmang. Buning o'rniga:

```bash
kubectl debug -it pod/api-7d9f --image=busybox --target=app
```

Bu vaqtinchalik konteyner Pod'ning process namespace'iga ulanadi.

Oxirida image'ni `trivy image` yoki `grype` bilan skanerlang — distroless'da odatda nol CVE chiqadi.
