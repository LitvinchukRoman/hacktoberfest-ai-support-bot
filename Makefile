DATABASE_URL ?= postgres://postgres:postgres@localhost:15432/skelar?sslmode=disable
TEST_DATABASE_URL ?= postgres://postgres:postgres@localhost:15432/skelar?sslmode=disable

.PHONY: build test test-integration test-all vet fmt lint migrate-up migrate-down db-up db-down db-reset

build:
	go build ./...

test:
	go test ./...

test-integration:
	TEST_DATABASE_URL=$(TEST_DATABASE_URL) go test ./internal/support/adapters/postgres/... -v

test-all: test test-integration

vet:
	go vet ./...

fmt:
	gofmt -l $$(find internal -name '*.go')

lint: vet fmt

migrate-up:
	migrate -path migrations -database "$(DATABASE_URL)" up

migrate-down:
	migrate -path migrations -database "$(DATABASE_URL)" down -all

db-up:
	docker compose up -d postgres
	@echo "waiting for postgres..."
	@until docker compose exec -T postgres pg_isready -U postgres >/dev/null 2>&1; do sleep 1; done
	$(MAKE) migrate-up

db-down:
	docker compose down -v

db-reset: migrate-down migrate-up
