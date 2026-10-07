# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
# nodynamic: webp uses its pure-Go codec instead of dlopen (distroless has no libc)
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    for c in app worker migrate; do \
      CGO_ENABLED=0 go build -tags nodynamic -trimpath -ldflags="-s -w" -o /out/$c ./cmd/$c || exit 1; \
    done

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/app /app
COPY --from=build /out/worker /worker
COPY --from=build /out/migrate /migrate
# Go sources only: the Spector API console builds its docs from them.
COPY --from=build /src/go.mod /src/go.mod
COPY --from=build /src/app /src/app
COPY --from=build /src/cmd /src/cmd
COPY --from=build /src/config /src/config
COPY --from=build /src/internal /src/internal
ENV DOCS_DIR=/src
EXPOSE 8080 9090
ENTRYPOINT ["/app"]
