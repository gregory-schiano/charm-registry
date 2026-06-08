GO      ?= go
BIN_DIR ?= $(CURDIR)/.bin
SHARED_DOCKER_NETWORK ?= charm-registry-shared

.PHONY: help fmt tidy tidy-check test test-race coverage vet build run lint vuln gosec sqlc-diff audit check integration-test up down down-clean generate-cert install-cert install-k8s-cert

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
		"make integration-test - run integration tests against a live stack (requires Docker)" \
		"make build        - build the registry and admin CLI binaries" \
		"make run          - run the registry locally" \
		"make generate-cert - generate the local embedded OCI TLS certificate" \
		"make install-cert - install the local embedded OCI certificate into system trust (requires sudo)" \
		"make install-k8s-cert - install the local embedded OCI certificate into Canonical k8s containerd trust (requires sudo)" \
		"make up           - start the local compose stack with embedded OCI registry" \
		"make down         - stop the local compose stack (preserves data)" \
"make down-clean   - stop the local compose stack and remove volumes"

fmt:
	$(GO) fmt $(_GO_PKGS)

tidy:
	$(GO) mod tidy
	$(GO) mod verify

# Ensures go.mod/go.sum are already tidy from the current worktree state.
# This stays useful both in CI and on feature branches with unrelated changes.
tidy-check:
	@before_mod=$$(cat go.mod); \
	before_sum=$$(cat go.sum); \
	$(GO) mod verify; \
	$(GO) mod tidy; \
	test "$$before_mod" = "$$(cat go.mod)"; \
	test "$$before_sum" = "$$(cat go.sum)"

# Packages to unit-test: exclude only sqlc-generated code (internal/repo/db).
_UNIT_PKGS = $(shell $(GO) list ./internal/... | grep -Ev '/repo/db$$')
_COVER_PKGS = $(shell $(GO) list -f '{{if .TestGoFiles}}{{.ImportPath}}{{else if .XTestGoFiles}}{{.ImportPath}}{{end}}' ./internal/... | grep -Ev '^$$|/repo/db$$')

# Repository Go packages. Keep package scope explicit instead of using `./...`
# so local runtime artifacts outside Go packages cannot affect tooling.
_GO_PKGS = ./cmd/... ./internal/...
_LINT_PKGS = $(_GO_PKGS)

test:
	$(GO) list ./internal/... | grep -Ev '/repo/db$$' | xargs $(GO) test

test-race:
	$(GO) list ./internal/... | grep -Ev '/repo/db$$' | xargs $(GO) test -race

coverage:
	@rm -f coverage.out
	@printf "mode: count\n" > coverage.out
	@for pkg in $(_COVER_PKGS); do \
		tmp_cov=$$(mktemp); \
		$(GO) test $$pkg -coverprofile=$$tmp_cov -covermode=count >/dev/null; \
		if [ -s "$$tmp_cov" ]; then tail -n +2 "$$tmp_cov" >> coverage.out; fi; \
		rm -f "$$tmp_cov"; \
	done
	$(GO) tool cover -func=coverage.out

vet:
	$(GO) vet $(_GO_PKGS)

build:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 $(GO) build -trimpath -buildvcs=true -o $(BIN_DIR)/charm-registry ./cmd/charm-registry
	CGO_ENABLED=0 $(GO) build -trimpath -buildvcs=true -o $(BIN_DIR)/charm-registryctl ./cmd/charm-registryctl

run:
	$(GO) run ./cmd/charm-registry

lint:
	$(GO) tool golangci-lint run $(_LINT_PKGS)

vuln:
	$(GO) tool govulncheck $(_GO_PKGS)

# internal/repo/db is sqlc-generated; G101 false-positives on SQL string
# constants are suppressed by excluding the directory from the scan.
gosec:
	$(GO) tool gosec -exclude-dir=internal/repo/db $(_GO_PKGS)

sqlc-diff:
	$(GO) tool sqlc diff

audit: tidy vet lint test vuln gosec

check: fmt audit

# Integration tests: run against a live charm-registry stack started via
# Docker Compose.  Requires Docker and the compose stack to be running
# (`make up`), or the CI workflow will start it for you.
# The -tags=integration flag selects only tests in tests/integration/.
integration-test:
	$(GO) test -tags=integration -count=1 ./tests/integration/...

generate-cert:
	bash ./deploy/oci/generate-certs.sh

install-cert: generate-cert
	@if command -v update-ca-certificates >/dev/null 2>&1; then \
		sudo cp certs/oci.crt /usr/local/share/ca-certificates/charm-registry-oci.crt && \
		sudo update-ca-certificates && \
		echo "OCI registry certificate installed (Debian/Ubuntu)."; \
	elif command -v update-ca-trust >/dev/null 2>&1; then \
		sudo cp certs/oci.crt /etc/pki/ca-trust/source/anchors/charm-registry-oci.crt && \
		sudo update-ca-trust extract && \
		echo "OCI registry certificate installed (Fedora/RHEL)."; \
	else \
		echo "Unsupported distro; add certs/oci.crt to your system trust store manually." && exit 1; \
	fi

install-k8s-cert: generate-cert
	bash ./deploy/k8s/install-oci-cert.sh

up: generate-cert
	docker network inspect $(SHARED_DOCKER_NETWORK) >/dev/null 2>&1 || docker network create $(SHARED_DOCKER_NETWORK)
	docker compose up --build -d postgres charm-registry

down:
	- docker compose down

down-clean:
	- docker compose down -v
