DATABASE_URL ?= postgres://postgres:postgres@localhost:15432/skelar?sslmode=disable
TEST_DATABASE_URL ?= postgres://postgres:postgres@localhost:15432/skelar?sslmode=disable

# Real HTTP handlers + Postgres queue + worker; isolated schema, no API key.
.PHONY: test-ops2
test-ops2:
	docker compose up -d --wait postgres
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test ./internal/support/adapters/httpapi -run '^TestOPS2Workflow$$' -v -count=1 -timeout=90s

.PHONY: support-eval support-kb build test test-integration test-all vet fmt lint migrate-up migrate-down db-up db-down db-reset run

build:
	go build ./...

run: db-up
	DATABASE_URL="$(DATABASE_URL)" SUPPORT_DEV_TOOLS=true PORT=8080 go run ./cmd/server

test:
	go test ./...

test-integration: db-up
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

# Classifier accuracy on the goldset. ARGS=-fake (keywords), ARGS=-smoke (what Gemma accepts), or empty (the model).
support-eval:
	go run ./cmd/support-eval $(ARGS)

# Rebuild the knowledge base from fantasm's articles: make support-kb FRONTEND=../fantasm/frontend
support-kb:
	go run ./cmd/support-kb $(FRONTEND)/content
