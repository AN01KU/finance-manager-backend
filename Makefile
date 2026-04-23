.PHONY: help build run test test-all test-integration cover lint fmt vet docker docker-down clean check

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

build: ## Build the binary
	go build -o finance-manager ./cmd/main.go

run: ## Run the server
	go run ./cmd/main.go

test: ## Run unit tests (no DB required)
	go test -race ./internal/middleware/... ./internal/config/... ./internal/recurring/... ./internal/helpers/...

test-all: ## Run all tests
	go test -race ./...

test-integration: ## Run integration tests (requires Postgres)
	go test -race ./internal/auth/... ./internal/group/...

cover: ## Run unit tests with coverage and open HTML report
	go test -race -coverprofile=coverage.out -covermode=atomic \
		./internal/middleware/... ./internal/config/... ./internal/recurring/... ./internal/helpers/...
	go tool cover -html=coverage.out -o coverage.html
	open coverage.html

lint: ## Check formatting and run go vet
	@unformatted=$$(gofmt -s -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "Files not formatted:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi
	go vet ./...

fmt: ## Format all Go files
	gofmt -s -w .

vet: ## Run go vet
	go vet ./...

docker: ## Start services with docker-compose
	docker-compose up --build

docker-down: ## Stop docker-compose services
	docker-compose down

clean: ## Remove built binary
	rm -f finance-manager

check: lint build test ## Pre-commit: fmt check + vet + build + unit tests
