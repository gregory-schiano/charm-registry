GO      ?= go
BIN_DIR ?= $(CURDIR)/.bin

.PHONY: help fmt tidy tidy-check test test-race coverage vet build run lint vuln gosec sqlc-diff audit check up down

help:
	@printf "%s\n" \
		"make fmt          - format Go code" \
		"make tidy         - tidy and verify Go modules" \
		"make tidy-check   - tidy, verify, and assert go.mod/go.sum are unchanged (CI)" \
		"make test         - run unit tests" \
		"make test-race    - run tests with the race detector" \
		"make coverage     - run tests with coverage and print report" \
		"make vet          - run go vet" \
		"make lint         - run golangci-lint" \
		"make vuln         - run govulncheck" \
		"make gosec        - run gosec static analysis" \
		"make sqlc-diff    - verify sqlc-generated code is up to date" \
		"make audit        - run lint, tests, and security checks" \
		"make build        - build the registry binary" \
		"make run          - run the registry locally" \
		"make up           - start the local compose stack" \
		"make down         - stop the local compose stack"

fmt:
	$(GO) fmt ./...

tidy:
	$(GO) mod tidy
	$(GO) mod verify

# Intended for CI: ensures go.mod/go.sum are already tidy and unmodified.
tidy-check:
	$(GO) mod verify
	$(GO) mod tidy
	git diff --exit-code go.mod go.sum

# Packages to unit-test: exclude infrastructure wiring (internal/app) that
# requires live external services, and sqlc-generated code (internal/repo/db).
_UNIT_PKGS = $(shell $(GO) list ./internal/... | grep -Ev '/(app|repo/db)$$')

test:
	$(GO) list ./internal/... | grep -Ev '/(app|repo/db)$$' | xargs $(GO) test

test-race:
	$(GO) list ./internal/... | grep -Ev '/(app|repo/db)$$' | xargs $(GO) test -race

coverage:
	$(GO) list ./internal/... | grep -Ev '/(app|repo/db)$$' | \
		xargs $(GO) test -race -coverprofile=coverage.out -covermode=atomic
	grep -v 'internal/repo/postgres' coverage.out > coverage_unit.out
	$(GO) tool cover -func=coverage_unit.out

vet:
	$(GO) vet ./...

build:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 $(GO) build -trimpath -buildvcs=true -o $(BIN_DIR)/charm-registry ./cmd/charm-registry

run:
	$(GO) run ./cmd/charm-registry

lint:
	$(GO) tool golangci-lint run

vuln:
	$(GO) tool govulncheck ./...

# internal/repo/db is sqlc-generated; G101 false-positives on SQL string
# constants are suppressed by excluding the directory from the scan.
gosec:
	$(GO) tool gosec -exclude-dir=internal/repo/db ./...

sqlc-diff:
	$(GO) tool sqlc diff

audit: tidy vet lint test vuln gosec

check: fmt audit

up:
	docker compose up --build

down:
	docker compose down -v
