GO ?= go
BIN_DIR ?= $(CURDIR)/.bin
PACKAGE ?= ./...

GOLANGCI_LINT := $(BIN_DIR)/golangci-lint
GOSEC := $(BIN_DIR)/gosec
GOVULNCHECK := $(BIN_DIR)/govulncheck

.PHONY: help fmt tidy test test-race vet build run lint vuln gosec audit check tools up down

help:
	@printf "%s\n" \
		"make fmt        - format Go code" \
		"make tidy       - tidy and verify Go modules" \
		"make test       - run unit tests" \
		"make test-race  - run tests with the race detector" \
		"make vet        - run go vet" \
		"make lint       - run golangci-lint" \
		"make vuln       - run govulncheck" \
		"make gosec      - run gosec static analysis" \
		"make audit      - run lint, tests, and security checks" \
		"make build      - build the registry binary" \
		"make run        - run the registry locally" \
		"make up         - start the local compose stack" \
		"make down       - stop the local compose stack"

fmt:
	$(GO) fmt ./...

tidy:
	$(GO) mod tidy
	$(GO) mod verify

test:
	$(GO) test $(PACKAGE)

test-race:
	$(GO) test -race $(PACKAGE)

vet:
	$(GO) vet $(PACKAGE)

build:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 $(GO) build -trimpath -buildvcs=true -o $(BIN_DIR)/charm-registry ./cmd/charm-registry

run:
	$(GO) run ./cmd/charm-registry

lint: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run

vuln: $(GOVULNCHECK)
	$(GOVULNCHECK) ./...

gosec: $(GOSEC)
	$(GOSEC) ./...

audit: tidy vet lint test vuln gosec

check: fmt audit

tools: $(GOLANGCI_LINT) $(GOSEC) $(GOVULNCHECK)

$(BIN_DIR):
	mkdir -p $(BIN_DIR)

$(GOLANGCI_LINT): | $(BIN_DIR)
	$(GO) build -o $(GOLANGCI_LINT) github.com/golangci/golangci-lint/v2/cmd/golangci-lint

$(GOSEC): | $(BIN_DIR)
	$(GO) build -o $(GOSEC) github.com/securego/gosec/v2/cmd/gosec

$(GOVULNCHECK): | $(BIN_DIR)
	$(GO) build -o $(GOVULNCHECK) golang.org/x/vuln/cmd/govulncheck

up:
	docker compose up --build

down:
	docker compose down -v
