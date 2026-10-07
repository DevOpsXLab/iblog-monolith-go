.DEFAULT_GOAL := help
.PHONY: help deps run worker migrate build admin seed test test-all cover vet lint lint-fix actionlint fmt tidy openapi images up up-seed ps down reset logs

BIN      ?= bin/app
TAG      ?= dev
USERNAME ?= admin
EMAIL    ?= admin@example.com

help: ## list targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*## "}{printf "  %-10s %s\n",$$1,$$2}'

deps: ## start postgres, redis, minio, mailpit
	docker compose up -d postgres redis minio mailpit

run: ## run API (migrations run on start)
	go run ./cmd/app

build: ## build binary into bin/
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BIN) ./cmd/app
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/worker ./cmd/worker
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/migrate ./cmd/migrate

admin: ## create super admin: make admin USERNAME=.. EMAIL=.. (password: ADMIN_PASSWORD or stdin)
	go run ./cmd/createadmin -username $(USERNAME) -email $(EMAIL)

seed: ## fill running stack with demo content (authors, posts, comments, claps...)
	go run ./cmd/seed -clean


worker: ## run background worker (pair with WORKER=false on the API)
	go run ./cmd/worker

migrate: ## apply app migrations, then Guard migrations + access seed
	go run ./cmd/migrate

test: ## unit tests (race detector on)
	go test -race -short ./...

test-all: ## unit + integration tests (needs Docker)
	go test -race ./...

cover: ## unit + integration coverage across packages (needs Docker); writes coverage.out
	go test -coverpkg=./... -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

vet: ## go vet
	go vet ./...

lint: ## golangci-lint v2 (.golangci.yml)
	golangci-lint run ./...

lint-fix: ## golangci-lint with autofix + gofmt/goimports
	golangci-lint run --fix ./...
	golangci-lint fmt ./...

actionlint: ## lint GitHub Actions workflows (.github/workflows)
	actionlint

fmt: ## gofmt
	gofmt -w .

tidy: ## go mod tidy
	go mod tidy

openapi: ## generate openapi.json with Spector CLI
	spector -dir . -o openapi.json

images: ## build release images (migrate, worker, api) tagged $(TAG)
	for t in migrate worker api; do docker build --target $$t -t iblog-$$t:$(TAG) . || exit 1; done

up: ## whole stack: infra -> migrate -> api + worker, waits until healthy
	docker compose up -d --build --wait

up-seed: ## demo data into the compose stack (dev profile, run once)
	docker compose --profile dev run --rm --build seed

ps: ## stack status and health
	docker compose ps -a

down: ## stop stack (data kept)
	docker compose down

reset: ## stop stack and DELETE its volumes (postgres, redis, minio data)
	docker compose --profile dev down -v

logs: ## follow api + worker logs
	docker compose logs -f api worker
