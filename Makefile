.PHONY: help run build fmt vet test test-integration test-all migrate up down logs tidy

help: ## Show available commands
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-18s %s\n", $$1, $$2}'

run: ## Run the API locally (needs PostgreSQL and Redis, see .env.example)
	go run ./cmd/api

build: ## Build the production binary into bin/api
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/api ./cmd/api

fmt: ## Format all Go files
	gofmt -w .

vet: ## Run go vet (including integration test files)
	go vet -tags integration ./...

test: ## Run unit tests (no external services needed)
	go test -race -count=1 ./...

test-integration: ## Run integration tests (needs PostgreSQL: make up)
	go test -tags integration -race -count=1 ./tests/...

test-all: test test-integration ## Run every test

migrate: ## Apply database migrations and exit
	go run ./cmd/api -migrate-only

up: ## Start API, PostgreSQL and Redis with Docker Compose
	docker compose up --build -d

down: ## Stop the Docker Compose stack
	docker compose down

logs: ## Follow API logs
	docker compose logs -f api

tidy: ## Tidy go.mod and go.sum
	go mod tidy
