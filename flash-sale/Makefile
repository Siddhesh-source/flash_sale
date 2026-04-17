BINARY   := flash-sale
CMD      := ./cmd/server
GO       := go
PKG      := ./...

.PHONY: all build run test test-chaos lint fmt vet tidy infra-up infra-down load-test clean help

all: tidy build

## build: compile the server binary into ./bin/
build:
	@mkdir -p bin
	$(GO) build -o bin/$(BINARY) $(CMD)

## run: run the server directly (requires infra to be up)
run:
	$(GO) run $(CMD)/main.go

## test: run all unit tests
test:
	$(GO) test ./internal/... -v -race -count=1

## test-chaos: run chaos tests (requires Docker)
test-chaos:
	$(GO) test ./chaos/... -v -timeout 10m

## test-cover: run tests with coverage report
test-cover:
	$(GO) test ./internal/... -race -coverprofile=coverage.txt -covermode=atomic
	$(GO) tool cover -html=coverage.txt -o coverage.html
	@echo "Coverage report: coverage.html"

## lint: run golangci-lint (requires golangci-lint to be installed)
lint:
	golangci-lint run $(PKG)

## fmt: format all Go source files
fmt:
	$(GO) fmt $(PKG)

## vet: run go vet
vet:
	$(GO) vet $(PKG)

## tidy: tidy and verify go modules
tidy:
	$(GO) mod tidy
	$(GO) mod verify

## infra-up: start all Docker services (Redis, Postgres, Jaeger, Prometheus, Grafana)
infra-up:
	docker-compose up -d
	@echo "Waiting for services to be ready..."
	@sleep 3
	@docker-compose ps

## infra-down: stop and remove all Docker services
infra-down:
	docker-compose down -v

## infra-logs: tail logs from all services
infra-logs:
	docker-compose logs -f

## load-test: run k6 load test (requires infra + server to be running)
load-test:
	docker-compose --profile load-test up k6

## redis-cli: open a Redis CLI shell on the primary
redis-cli:
	docker-compose exec redis redis-cli

## psql: open a psql shell
psql:
	docker-compose exec postgres psql -U user -d flashsale

## clean: remove build artifacts
clean:
	rm -rf bin/ coverage.txt coverage.html tmp/

## help: show this help
help:
	@grep -E '^##' Makefile | sed 's/## /  /'
