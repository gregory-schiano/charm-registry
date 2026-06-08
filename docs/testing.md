# Testing

The registry uses a layered testing approach: unit tests, static analysis, fuzz tests, a shared functional test harness, charm integration tests (Jubilant), and snap integration tests (spread).

## Unit tests

```bash
make test
```

Runs `go test` on all internal packages, excluding the sqlc-generated `internal/repo/db` package. Uses table-driven tests and the in-memory repository to avoid requiring a live database.

### With race detector

```bash
make test-race
```

### Coverage

```bash
make coverage
```

Produces a coverage report via `go tool cover -func=coverage.out`.

Postgres-specific tests require a live database — set `CHARM_REGISTRY_DATABASE_URL` to run them.

## Static analysis

| Command | Tool | Purpose |
|---------|------|---------|
| `make vet` | `go vet` | Correctness checks |
| `make lint` | `golangci-lint` | Curated rule set (`.golangci.yml`), Juju-inspired |
| `make vuln` | `govulncheck` | Known vulnerabilities in dependencies |
| `make gosec` | `gosec` | Security-focused static analysis |
| `make tidy-check` | `go mod` | Verify go.mod/go.sum are tidy (CI gate) |
| `make sqlc-diff` | `sqlc` | Verify generated code matches SQL queries |
| `make audit` | all | Runs lint, tests, and security checks in sequence |

## Fuzz tests

Security-sensitive parsers have fuzz tests:

```bash
go test -fuzz=FuzzParseArchive ./internal/charm/
go test -fuzz=FuzzParseMacaroon ./internal/auth/
```

## Functional test harness

The shared functional test harness (`cmd/functional-test/` and `tests/functional/`) runs endpoint-driven scenarios against any deployed instance — Juju charm, snap, or local process.

### What it covers

16 scenarios exercising: health/readiness, admin bootstrap, token lifecycle, package registration, revision upload/download, release channels, resource handling, OCI registry push/pull, and sync rules.

### Configuration

| Variable | Purpose | Default |
|----------|---------|---------|
| `FTEST_API_URL` | API base URL | `http://localhost:8080` |
| `FTEST_OCI_URL` | OCI registry URL | `https://localhost:15000` |
| `FTEST_OCI_CERT_PATH` | OCI CA cert PEM path | _(empty)_ |
| `FTEST_ADMIN_SUBJECT` | Dev-auth subject | `admin` |
| `FTEST_ADMIN_USER` | Dev-auth username | `admin` |

### Running directly

```bash
# Build and run against a local instance:
go build -o .bin/functional-test ./cmd/functional-test
FTEST_API_URL=http://localhost:8080 .bin/functional-test

# Against a remote Juju-deployed instance:
FTEST_API_URL=http://10.0.0.5:8080 .bin/functional-test
```

### Running as Go tests

```bash
FTEST_API_URL=http://localhost:8080 go test -tags=functional -v -count=1 ./tests/functional/...
```

The charm and snap integration suites invoke this binary automatically as part of their test flows.

## Charm integration tests (Jubilant)

Charm integration tests deploy charm-registry through Juju using [Jubilant](https://github.com/canonical/jubilant), exercise the deployed charm's endpoints, and verify restart/persistence behavior through Juju unit reschedule.

### Prerequisites

- `juju` CLI with a bootstrapped controller (microk8s or LXD)
- `charmcraft`
- Python 3 with `jubilant` and `pytest`

### Running

```bash
make charm-pack              # Pack the charm first
make charm-integration-test  # Run the Jubilant suite
```

Or directly:

```bash
cd tests/integration/charm
python3 -m pytest -v -s --tb native
```

### What it covers

- Deploys `postgresql-k8s`, `traefik-k8s`, and optionally `s3-integrator`
- Deploys the local `charm-registry` charm
- Waits for `active/idle` workload status
- Runs the shared functional-test binary against the deployed endpoint
- Verifies data persists across Juju unit reschedule

### CI

The charm integration suite runs in `.github/workflows/integration.yml` on a schedule and manual dispatch, using microk8s on a GitHub Actions runner.

## Snap integration tests (spread)

Snap integration tests use [spread](https://github.com/snapcore/spread) to install the charm-registry snap in a clean VM, configure it, and exercise the same functional scenarios.

### Prerequisites

- `spread` CLI
- `snapcraft`
- LXD or QEMU backend

### Running

```bash
make snap-pack               # Pack the snap first
make snap-integration-test   # Run the spread suite
```

### Systems

Currently targets `ubuntu-24.04-amd64` on LXD and QEMU backends (configured in `spread.yaml`).

### What it covers

- Packs and installs the snap in dangerous mode
- Configures insecure dev-auth and public URLs via `snap set`
- Waits for the service health endpoint
- Builds and runs the shared functional-test binary
- Verifies all 16 functional scenarios pass

### CI

Spread tests run in `.github/workflows/integration.yml` alongside the charm integration suite.

## Test isolation

Unit tests use the in-memory repository (`internal/repo/memory.go`) to avoid requiring a live database. Postgres-specific tests use a helper that connects to a real Postgres instance — set `CHARM_REGISTRY_DATABASE_URL` to run them.
