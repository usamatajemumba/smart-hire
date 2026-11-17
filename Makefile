# SmartHire developer commands. Run `make help` to list them.
SERVICES := job-service candidate-service

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

## ---------- Docker (full local environment) ----------
.PHONY: up
up: ## Build and start postgres + all services in the background
	docker compose up -d --build --wait

.PHONY: down
down: ## Stop containers (keeps data volumes)
	docker compose down

.PHONY: reset
reset: ## Stop containers AND delete volumes (wipes the database)
	docker compose down -v

.PHONY: logs
logs: ## Follow logs of all services
	docker compose logs -f

.PHONY: ps
ps: ## Show container status
	docker compose ps

.PHONY: psql-jobs
psql-jobs: ## Open psql on the jobs database
	docker compose exec postgres psql -U smarthire -d smarthire_jobs

.PHONY: psql-candidates
psql-candidates: ## Open psql on the candidates database
	docker compose exec postgres psql -U smarthire -d smarthire_candidates

## ---------- Go (run on host) ----------
.PHONY: build
build: ## Build all service binaries into ./bin
	@for s in $(SERVICES); do go build -o bin/$$s ./services/$$s/cmd || exit 1; done

.PHONY: db
db: ## Start only postgres (for running services on host)
	docker compose up -d --wait postgres

.PHONY: run-jobs
run-jobs: ## Run job-service on host (needs `make db`)
	go run ./services/job-service/cmd

.PHONY: run-candidates
run-candidates: ## Run candidate-service on host (needs `make db`)
	go run ./services/candidate-service/cmd

.PHONY: test
test: ## Run unit tests with the race detector
	go test -race -count=1 ./...

.PHONY: cover
cover: ## Run tests with coverage summary
	go test -race -coverprofile=coverage.out ./... && go tool cover -func=coverage.out | tail -1

.PHONY: lint
lint: ## go vet + gofmt check (+ golangci-lint if installed)
	go vet ./...
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)
	@if command -v golangci-lint >/dev/null; then golangci-lint run ./...; else echo "golangci-lint not installed, skipped"; fi

.PHONY: tidy
tidy: ## go mod tidy
	go mod tidy
