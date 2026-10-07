.DEFAULT_GOAL := help
.PHONY: help deps run worker migrate build admin seed test test-all cover vet fmt tidy openapi up down logs

BIN      ?= bin/app
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

migrate: ## apply migrations and exit
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

fmt: ## gofmt
	gofmt -w .

tidy: ## go mod tidy
	go mod tidy

openapi: ## generate openapi.json with Spector CLI
	spector -dir . -o openapi.json

up: ## whole stack
	docker compose up -d --build

down: ## stop stack
	docker compose down

logs: ## follow api logs
	docker compose logs -f api
