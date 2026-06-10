GO      ?= go
BIN_DIR ?= $(CURDIR)/.bin

.PHONY: help fmt tidy tidy-check test test-race coverage vet build run lint vuln gosec sqlc-diff audit check generate-cert install-cert install-k8s-cert charm-pack rock-pack rock-smoke-test snap-pack artifact-build functional-test functional-test-build charm-integration-test snap-integration-test

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
		"" \
		"make functional-test       - run shared functional scenarios against FTEST_API_URL" \
		"make functional-test-build - compile the functional-test binary" \
		"" \
		"make charm-pack            - pack the charm with charmcraft" \
		"make rock-pack             - pack the OCI rock with rockcraft" \
		"make rock-smoke-test       - inspect and validate a built .rock artifact" \
		"make snap-pack             - pack the snap with snapcraft" \
		"make artifact-build        - build all artifacts (charm, rock, snap)" \
		"make charm-integration-test - run charm integration tests with Jubilant" \
		"make snap-integration-test  - run snap spread tests (requires spread + snapd)"

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

# ---------- Artifact packaging ----------

charm-pack:
	cd charm && charmcraft pack

rock-pack:
	rockcraft pack

rock-smoke-test:
	bash scripts/rock-smoke-test.sh $(ROCK_FILE)

snap-pack:
	snapcraft pack

artifact-build: charm-pack rock-pack snap-pack

# ---------- Integration tests ----------

charm-integration-test:
	@if command -v juju >/dev/null 2>&1 && command -v lxc >/dev/null 2>&1; then \
		echo "Running Jubilant charm integration tests..."; \
		cd tests/integration/charm && python3 -m pytest -v -s --tb native --log-cli-level=INFO; \
	else \
		echo "BLOCKED: Juju and/or LXD not found — cannot run charm integration tests." && \
		echo "Prerequisites: juju (snap install juju --classic) and lxc (snap install lxd)." && \
		exit 1; \
	fi

snap-integration-test:
	@if command -v spread >/dev/null 2>&1; then \
		spread -v ./tests/spread/...; \
	else \
		echo "ERROR: spread not found — install it (snap install spread --classic) or run in CI."; \
		exit 1; \
	fi
