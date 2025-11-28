.PHONY: help generate build run test clean docker-build docker-run docker-compose-up docker-compose-down migrate-up migrate-down pprof-cpu pprof-heap pprof-goroutine

help: ## Display this help screen
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

generate: ## Generate code from proto files and sync vendored protos
	buf generate buf.build/loci/loci-proto
	go mod tidy
	go mod vendor

build: ## Build the application
	go build -o bin/server cmd/server/main.go

run: ## Run the application
	go run cmd/server/main.go

test: ## Run tests
	go test -v -race -coverprofile=coverage.out ./...

test-coverage: test ## Run tests with coverage report
	go tool cover -html=coverage.out

clean: ## Clean build artifacts
	rm -rf bin/
	rm -f coverage.out
	rm -f *.prof

deps: ## Download dependencies
	go mod download
	go mod tidy

lint: ## Run linters
	golangci-lint run ./...

# Docker commands
docker-build: ## Build Docker image
	docker build -t loci-api:latest .

docker-run: ## Run Docker container
	docker run -p 8080:8080 --env-file .env loci-api:latest

# Docker Compose commands
docker-compose-up: ## Start all services with docker-compose
	docker-compose up -d

docker-compose-down: ## Stop all services
	docker-compose down

docker-compose-logs: ## View logs from all services
	docker-compose logs -f

docker-compose-restart: ## Restart all services
	docker-compose restart

# Database commands
migrate-up: ## Run database migrations
	psql $(DATABASE_URL) -f migrations/001_create_myservice_table.sql

migrate-down: ## Rollback database migrations
	psql $(DATABASE_URL) -c "DROP TABLE IF EXISTS myservice_records;"

# Development
dev: ## Run with hot reload (requires air)
	air

# Profiling commands (requires PPROF_ENABLED=true)
pprof-cpu: ## Profile CPU for 30 seconds
	go tool pprof -http=:8081 http://localhost:6060/debug/pprof/profile?seconds=30

pprof-heap: ## Profile heap memory
	go tool pprof -http=:8082 http://localhost:6060/debug/pprof/heap

pprof-goroutine: ## Show goroutine profile
	curl http://localhost:6060/debug/pprof/goroutine?debug=2

pprof-save: ## Save all profiles to files
	@echo "Saving profiles..."
	@curl -s -o cpu.prof http://localhost:6060/debug/pprof/profile?seconds=30 &
	@curl -s -o heap.prof http://localhost:6060/debug/pprof/heap
	@curl -s -o goroutine.prof http://localhost:6060/debug/pprof/goroutine
	@echo "Profiles saved: cpu.prof, heap.prof, goroutine.prof"

.DEFAULT_GOAL := help
