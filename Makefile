GO      ?= go
BIN_DIR ?= $(CURDIR)/.bin
SHARED_DOCKER_NETWORK ?= charm-registry-shared

.PHONY: help fmt tidy tidy-check test test-race coverage vet build run lint vuln gosec sqlc-diff audit check up down generate-cert install-cert install-k8s-cert integration-certs integration-test integration-up integration-down integration-run functional-test functional-test-build charm-pack rock-pack snap-pack artifact-build charm-integration-test snap-integration-test

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
		"make build        - build the registry and admin CLI binaries" \
		"make run          - run the registry locally" \
		"make generate-cert - generate the local embedded OCI TLS certificate" \
		"make install-cert - install the local embedded OCI certificate into system trust (requires sudo)" \
		"make install-k8s-cert - install the local embedded OCI certificate into Canonical k8s containerd trust (requires sudo)" \
		"make up           - start the local compose stack with embedded OCI registry" \
		"make down         - stop the local compose stack" \
		"" \
		"make integration-up    - start the integration test Docker Compose stack" \
		"make integration-down  - stop the integration test Docker Compose stack" \
		"make integration-run   - run integration tests against the running stack" \
		"make integration-test  - start stack, wait for healthy, run tests, stop stack" \
		"make integration-certs - generate OCI TLS certs and make key readable for integration containers" \
		"" \
		"make functional-test       - run shared functional scenarios against FTEST_API_URL" \
		"make functional-test-build - compile the functional-test binary" \
		"" \
		"make charm-pack              - pack the charm" \
		"make rock-pack               - pack the rock OCI image" \
		"make snap-pack               - pack the snap" \
		"make artifact-build          - build all artifacts (charm, rock, snap)" \
		"make charm-integration-test  - run charm integration tests (requires Juju/LXD)" \
		"make snap-integration-test   - run snap spread tests (requires snapd/LXD)"

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
	- docker compose down -v

# ---------- Integration tests ----------

COMPOSE_ITEST = docker compose -f compose.integration.yaml
ITEST_TIMEOUT = 120

# Integration certs: generate then make key readable by the
# nonroot container user (uid 65534) that the distroless image runs as.
# generate-cert sets oci.key to 0640 (owner+group), but the bind mount
# serves the host filesystem directly, so the container process — which
# is neither the host user nor the host docker group — cannot read it.
integration-certs: generate-cert
	chmod 0644 certs/oci.key

integration-up: integration-certs
	$(COMPOSE_ITEST) up --build -d
	bash ./scripts/wait-for-healthy.sh http://localhost:18080/healthz $(ITEST_TIMEOUT)

integration-down:
	- $(COMPOSE_ITEST) down -v

integration-run:
	$(GO) test -tags=integration -count=1 -timeout=10m -v ./tests/integration/...

integration-test: integration-up integration-run integration-down

# ---------- Functional test harness ----------
# Runs endpoint-driven functional scenarios against any running instance.
# Configure with FTEST_* environment variables (see tests/functional/README.md).

FTEST_API_URL ?= http://localhost:8080

functional-test-build:
	$(GO) build -o $(BIN_DIR)/functional-test ./cmd/functional-test

functional-test: functional-test-build
	FTEST_API_URL=$(FTEST_API_URL) $(BIN_DIR)/functional-test

# Run functional scenarios as Go tests (alternative to the compiled binary):
#   FTEST_API_URL=http://10.0.0.5:8080 go test -tags=functional -v ./tests/functional/...

# ---------- Artifact packaging (stubs — implemented by T05/T06/T07) ----------

charm-pack:
	@echo "charm-pack: pack charm (requires charmcraft)" && \
	if command -v charmcraft >/dev/null 2>&1; then \
		cd charm && charmcraft pack; \
	else \
		echo "ERROR: charmcraft not found — install it or run this in a CI environment with charmcraft available." && exit 1; \
	fi

rock-pack:
	@echo "rock-pack: pack rock OCI image (requires rockcraft)" && \
	if command -v rockcraft >/dev/null 2>&1; then \
		rockcraft pack; \
	else \
		echo "ERROR: rockcraft not found — install it or run this in a CI environment with rockcraft available." && exit 1; \
	fi

snap-pack:
	@echo "snap-pack: pack snap (requires snapcraft)" && \
	if command -v snapcraft >/dev/null 2>&1; then \
		cd snap && snapcraft pack; \
	else \
		echo "ERROR: snapcraft not found — install it or run this in a CI environment with snapcraft available." && exit 1; \
	fi

artifact-build: charm-pack rock-pack snap-pack

charm-integration-test:
	@echo "charm-integration-test: run charm integration tests (requires Juju + LXD)" && \
	if command -v juju >/dev/null 2>&1 && command -v lxc >/dev/null 2>&1; then \
		echo "Running Jubilant charm integration tests..."; \
		cd tests/integration && go test -tags=functional -count=1 -timeout=30m -v .; \
	else \
		echo "ERROR: Juju and/or LXD not found — prerequisite missing." && exit 1; \
	fi

snap-integration-test:
	@echo "snap-integration-test: run snap spread tests (requires snapd + LXD)" && \
	if command -v snap >/dev/null 2>&1 && command -v lxd >/dev/null 2>&1; then \
		if [ -f spread.yaml ]; then spread -v ./tests/spread/...; \
		else echo "ERROR: spread.yaml not found — not yet implemented by T06." && exit 1; fi; \
	else \
		echo "ERROR: snapd and/or LXD not found — prerequisite missing." && exit 1; \
	fi
