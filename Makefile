SHELL := /bin/bash
BINARY := kpccssl
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)
DEV_DATA := $(CURDIR)/.data

.PHONY: help deps web build dev dev-api dev-web test test-go test-web lint e2e docker docker-run migrate-up sqlc scan clean

help:
	@echo "KPCCSSL targets:"
	@echo "  make deps        install Go and npm dependencies"
	@echo "  make web         build the React SPA into internal/webui/dist"
	@echo "  make build       build bin/kpccssl with the embedded SPA"
	@echo "  make dev         run API (fake ACME) and Vite together"
	@echo "  make test        run Go and frontend tests"
	@echo "  make lint        go vet + frontend typecheck"
	@echo "  make e2e         Playwright end-to-end tests"
	@echo "  make docker      build the container image"
	@echo "  make migrate-up  apply database migrations"
	@echo "  make scan        govulncheck + npm audit"

deps:
	go mod download
	cd web && npm ci --no-audit --no-fund

web:
	cd web && npm run build

build: web
	mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/kpccssl
	@echo "built bin/$(BINARY) ($(VERSION))"

dev:
	@trap 'kill 0' EXIT INT TERM; \
	$(MAKE) dev-api & \
	$(MAKE) dev-web & \
	wait

dev-api:
	KPCC_DEV=1 KPCC_FAKE_ACME=$${KPCC_FAKE_ACME:-1} KPCC_DATA_DIR=$(DEV_DATA) \
	KPCC_DEPLOY_ROOTS=$(DEV_DATA)/deploy \
	go run ./cmd/kpccssl serve

dev-web:
	cd web && npm run dev

test: test-go test-web

test-go:
	go test ./...

test-web:
	cd web && npm test

lint:
	@out="$$(gofmt -l cmd internal migrations)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	go vet ./...
	cd web && npm run typecheck

e2e: web
	cd web && npx playwright install --with-deps chromium >/dev/null 2>&1 || true
	cd web && npm run e2e

docker:
	docker build -f deploy/Dockerfile -t kpccssl:$(VERSION) .

docker-run:
	docker run --rm -p 8080:8080 -v kpccssl-data:/data --name kpccssl kpccssl:$(VERSION)

migrate-up: build
	./bin/$(BINARY) migrate

sqlc:
	@command -v sqlc >/dev/null || (echo "install sqlc: go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest" && exit 1)
	sqlc generate

scan:
	@command -v govulncheck >/dev/null && govulncheck ./... || echo "govulncheck not installed"
	cd web && npm audit --audit-level=high || true

clean:
	rm -rf bin .data
	cd web && rm -rf node_modules
