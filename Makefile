# Viro — developer Makefile
# Most targets are thin wrappers so the local dev loop is one command each.

API_DIR := apps/api
WEB_DIR := apps/web
GO      ?= go

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

## ----- Local dependencies (Docker) -----
.PHONY: dev-up dev-down dev-logs
dev-up: ## Start local Postgres + Redis (docker compose)
	docker compose -f docker/docker-compose.yml up -d
dev-down: ## Stop local dependencies
	docker compose -f docker/docker-compose.yml down
dev-logs: ## Tail local dependency logs
	docker compose -f docker/docker-compose.yml logs -f

## ----- Go control-plane (apps/api) -----
.PHONY: api-run api-build api-test api-vet api-tidy
api-run: ## Run the Go API (http://localhost:8080)
	cd $(API_DIR) && $(GO) run ./cmd/api
api-build: ## Build the API binary into apps/api/bin/
	cd $(API_DIR) && $(GO) build -o bin/vortex-api ./cmd/api
api-test: ## Run Go unit tests
	cd $(API_DIR) && $(GO) test ./...
api-vet: ## Vet the Go code
	cd $(API_DIR) && $(GO) vet ./...
api-tidy: ## Tidy Go modules
	cd $(API_DIR) && $(GO) mod tidy

## ----- Web UI (apps/web) -----
.PHONY: web-dev web-build web-test web-install
web-install: ## Install web dependencies
	cd $(WEB_DIR) && npm install
web-dev: ## Run the Next.js dev server (http://localhost:3000)
	cd $(WEB_DIR) && npm run dev
web-build: ## Production-build the web app
	cd $(WEB_DIR) && npm run build
web-test: ## Run web unit tests
	cd $(WEB_DIR) && npm test --silent

## ----- Aggregate -----
.PHONY: test build
test: api-test ## Run all unit tests
build: api-build ## Build all services
