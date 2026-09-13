#!/usr/bin/make
# Every target works without Docker: the project builds and tests on a plain Go + Node
# installation. (The upstream Makefile drove everything through `docker compose`, which is
# why it was replaced.)

.PHONY: help build frontend test test-go test-web dev migrate migrate-check seed run clean accept

# Stamped into the binary so a running instance can answer "which build am I?".
# `git describe` gives the tag, or the short commit hash when there is none; -dirty
# catches the case of a binary built from modified sources - which is exactly the one
# you want to know about when debugging.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
PKG := github.com/yuandzhang/webhook-zq/internal/version
GO_LDFLAGS := -s -w -X '$(PKG).version=$(VERSION)' -X '$(PKG).buildTime=$(BUILD_TIME)'

help: ## Show this help
	@printf "\033[33m%s:\033[0m\n" 'Available commands'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[32m%-12s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build the server binary (frontend is embedded at build time)
	# web/embed.go does //go:embed dist, so a clean clone - which has no web/dist yet -
	# would fail to build. The generator writes the placeholder only when the file is
	# absent, so it is safe to run every time and never clobbers a real build.
	go generate ./web/...
	go build -trimpath -ldflags '$(GO_LDFLAGS)' -o ./bin/webhook-zq ./cmd/webhook-tester

frontend: ## Build the single page application into web/dist
	npm --prefix ./web install --no-audit --no-fund
	npm --prefix ./web run build

test: test-go test-web ## Run every test

test-go: ## Go unit tests (no database required)
	go test -race ./internal/... ./cmd/...

test-web: ## Frontend unit tests
	npm --prefix ./web run test

# End-to-end acceptance: hits a real PostgreSQL and a real server process, which the Go
# unit tests deliberately do not. Set DATABASE_URL first; the scripts start their own
# instance on a free port.
#
# Skipped rather than failed when PowerShell is absent: these scripts use
# Get-NetTCPConnection, a Windows-only cmdlet, so on Linux/macOS there is nothing for
# them to do even if a shell were available. A CI that is red on day one is worse than
# no CI at all.
accept: ## Acceptance scripts (Windows only: they use Get-NetTCPConnection)
	@PS=$$(command -v pwsh 2>/dev/null || command -v powershell 2>/dev/null); \
	if [ -z "$$PS" ]; then \
		echo "skip: acceptance needs PowerShell (the scripts use Get-NetTCPConnection, a Windows-only cmdlet)"; \
		exit 0; \
	fi; \
	"$$PS" ./scripts/acceptance/b1-inbox-capture.ps1 && \
	"$$PS" ./scripts/acceptance/b2-replay.ps1 && \
	"$$PS" ./scripts/acceptance/b3-retention-tenant.ps1 && \
	"$$PS" ./scripts/acceptance/demo-path.ps1

dev: ## Start API + UI with one command (see also ./dev.ps1)
	pwsh ./dev.ps1

run: ## Run the server (set DATABASE_URL first)
	go run ./cmd/webhook-tester start

migrate: ## Apply the schema without starting the server
	go run ./cmd/webhook-tester migrate --database-url "$${DATABASE_URL}"

migrate-check: ## Fail if the database is not at the expected schema version
	go run ./cmd/webhook-tester migrate --check --database-url "$${DATABASE_URL}"

seed: ## Load the demo data (needs psql on PATH and DATABASE_URL set)
	psql "$${DATABASE_URL}" -f scripts/sample-data.sql

clean: ## Remove build artefacts
	rm -rf ./bin ./web/dist
