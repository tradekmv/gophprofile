.PHONY: help init up down restart logs ps build test test-coverage lint \
        migrate-up migrate-down run-server run-worker smoke clean

SHELL := /bin/bash
export GOMODCACHE    := $(CURDIR)/.gocache/mod
export GOCACHE       := $(CURDIR)/.gocache/build
export GOSUMDB       := off
export GOFLAGS       := -mod=mod

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

init: ## Initialize project: clone SPA template into web/
	@if [ ! -d "web" ]; then \
		echo "Cloning SPA template..."; \
		git clone --depth=1 https://github.com/Yandex-Practicum/go-avatar-service-template.git web-tmp; \
		mv web-tmp/web web/ 2>/dev/null || mv web-tmp/* web/; \
		rm -rf web-tmp; \
		echo "web/ ready"; \
	else \
		echo "web/ already exists, skipping"; \
	fi

up: ## Start all services in docker-compose
	docker compose up -d
	@echo "Waiting for services to be healthy..."
	@docker compose ps

down: ## Stop all services
	docker compose down

restart: ## Restart all services
	docker compose restart

logs: ## Tail logs from all services
	docker compose logs -f

ps: ## List running services
	docker compose ps

build: ## Build server, worker and migrate binaries
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/server ./cmd/server
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/worker ./cmd/worker
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/migrate ./cmd/migrate

test: ## Run unit tests
	go test -race -timeout 120s ./...

test-coverage: ## Run tests with coverage report
	go test -race -coverprofile=coverage.out -timeout 120s ./...
	go tool cover -func=coverage.out | tail -1
	go tool cover -html=coverage.out -o coverage.html

lint: ## Run golangci-lint (auto-installs to ./bin if missing)
	@if [ ! -x bin/golangci-lint ]; then \
		echo "Installing golangci-lint to ./bin ..."; \
		GOBIN=$(CURDIR)/bin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.4.0; \
	fi
	./bin/golangci-lint run ./...

migrate-up: ## Apply database migrations (requires DATABASE_DSN)
	@if [ -z "$$DATABASE_DSN" ]; then \
		echo "DATABASE_DSN is required"; exit 1; \
	fi
	go run ./cmd/migrate up

migrate-down: ## Rollback last database migration
	@if [ -z "$$DATABASE_DSN" ]; then \
		echo "DATABASE_DSN is required"; exit 1; \
	fi
	go run ./cmd/migrate down

run-server: ## Run server locally (requires .env)
	@if [ -f .env ]; then set -a && . ./.env && set +a; fi
	go run ./cmd/server

run-worker: ## Run worker locally (requires .env)
	@if [ -f .env ]; then set -a && . ./.env && set +a; fi
	go run ./cmd/worker

smoke: ## Run end-to-end smoke test (requires stack up)
	bash scripts/smoke.sh

clean: ## Stop stack and remove build artifacts
	docker compose down -v
	rm -rf bin/ coverage.out coverage.html
