# ============================================================
# PRODO Backend — Makefile
# Jalankan: make <target>
# ============================================================

APP_NAME     := prodo-backend
BINARY       := bin/$(APP_NAME)
MAIN         := cmd/api/main.go
MIGRATE      := migrate -path migrations -database "$(DATABASE_URL)"
LINT_VERSION := v1.59.1

.PHONY: all build run dev clean test test.unit test.cover \
        db.migrate migrate-up db.rollback db.status db.seed db.reset \
        lint fmt vet tidy docker.up docker.down help

## ── Build ──────────────────────────────────────────────────

all: build

build:
	@echo "→ Building $(APP_NAME)..."
	@mkdir -p bin
	go build -ldflags="-s -w" -o $(BINARY) $(MAIN)
	@echo "✓ Binary: $(BINARY)"

run: build
	@./$(BINARY)

dev:
	@echo "→ Starting hot reload (Air)..."
	@which air > /dev/null || (echo "Install Air: go install github.com/air-verse/air@latest" && exit 1)
	air

clean:
	@rm -rf bin/ tmp/ coverage.out

## ── Test ───────────────────────────────────────────────────

test:
	go test ./... -race -count=1

test.unit:
	go test ./internal/... -race -count=1 -short

test.cover:
	go test ./... -race -coverprofile=coverage.out -covermode=atomic
	go tool cover -html=coverage.out -o coverage.html
	@echo "→ Coverage report: coverage.html"
	@go tool cover -func=coverage.out | grep total

## ── Database / Migration ───────────────────────────────────

db.migrate:
	@echo "→ Running migrations up..."
	$(MIGRATE) up
	@echo "✓ Migrations applied"

# Alias -- docs/sprint_backlog.md S0-19 DoD menyebut "make migrate-up" persis;
# db.migrate tetap nama utama (konsisten dengan db.rollback/db.status/dst).
migrate-up: db.migrate

db.rollback:
	@echo "→ Rolling back last migration..."
	$(MIGRATE) down 1

db.status:
	$(MIGRATE) version

db.seed:
	@echo "→ Loading seed data..."
	psql "$(DATABASE_URL)" -f seeds/01_platform_admin.sql
	psql "$(DATABASE_URL)" -f seeds/02_demo_orgs.sql
	psql "$(DATABASE_URL)" -f seeds/03_demo_tasks.sql
	@echo "✓ Seed data loaded"

db.reset:
	@echo "⚠ WARNING: This drops and recreates the database!"
	@read -p "Continue? [y/N] " confirm && [ "$$confirm" = "y" ]
	$(MIGRATE) drop -f
	$(MIGRATE) up
	@$(MAKE) db.seed

## ── Code Quality ───────────────────────────────────────────

lint:
	@which golangci-lint > /dev/null || (echo "Install: https://golangci-lint.run/usage/install/" && exit 1)
	golangci-lint run ./...

fmt:
	gofmt -w .
	goimports -w .

vet:
	go vet ./...

tidy:
	go mod tidy

## ── Docker ─────────────────────────────────────────────────

docker.up:
	@echo "→ Starting dev stack (Docker Compose)..."
	docker compose -f ../infra/docker-compose.dev.yml up -d
	@echo "✓ Stack running. Check status: make docker.ps"

docker.down:
	docker compose -f ../infra/docker-compose.dev.yml down

docker.ps:
	docker compose -f ../infra/docker-compose.dev.yml ps

docker.logs:
	docker compose -f ../infra/docker-compose.dev.yml logs -f

## ── Help ───────────────────────────────────────────────────

help:
	@echo ""
	@echo "PRODO Backend — Available targets:"
	@echo "  make build        Build binary ke bin/"
	@echo "  make dev          Hot reload dengan Air"
	@echo "  make test         Jalankan semua test"
	@echo "  make test.cover   Test + coverage report"
	@echo "  make db.migrate   Jalankan migration"
	@echo "  make db.rollback  Rollback 1 migration"
	@echo "  make db.seed      Load seed data"
	@echo "  make db.reset     Drop + migrate + seed (HAPUS DATA!)"
	@echo "  make lint         golangci-lint"
	@echo "  make docker.up    Start dev Docker stack"
	@echo "  make docker.down  Stop dev Docker stack"
	@echo ""
