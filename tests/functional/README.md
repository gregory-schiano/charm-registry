# Shared Functional Test Harness

Endpoint-driven functional tests for charm-registry that run against any
deployed instance — Juju charm, snap package, or local process — without
invoking a local container-stack orchestrator.

## Overview

This package extracts the core API-interaction logic from the
`tests/integration/` suite into reusable scenario functions. Each scenario
exercises a specific behavior (health check, token lifecycle, package
registration, revision upload, release channels) against a configured API
endpoint.

Callers include:

- **Go tests** (`go test -tags=functional ./tests/functional/...`) — used by
  any harness that can set environment variables before the test binary runs.
- **Compiled binary** (`go run ./cmd/functional-test/`) — used by spread
  (snap) tests and shell scripts.
- **Jubilant charm integration** — the charm test suite can invoke the
  binary after deploying charm-registry via Juju.

## Configuration

All parameters are supplied via environment variables:

| Variable | Description | Default |
|---|---|---|
| `FTEST_API_URL` | Base URL of the charm-registry API | `http://localhost:8080` |
| `FTEST_OCI_URL` | Base URL of the OCI registry | `https://localhost:15000` |
| `FTEST_OCI_CERT_PATH` | Path to OCI registry CA cert PEM; optional for plain HTTP OCI URLs | _(empty)_ |
| `FTEST_ADMIN_SUBJECT` | Dev-auth subject | `admin` |
| `FTEST_ADMIN_USER` | Dev-auth username | `admin` |

## Running

### As Go tests

```sh
# Against a locally running service (Juju, snap, or bare binary):
FTEST_API_URL=http://localhost:8080 go test -tags=functional -v -count=1 ./tests/functional/...

# Against a remote Juju-deployed instance:
FTEST_API_URL=http://10.0.0.5:8080 FTEST_ADMIN_SUBJECT=ops go test -tags=functional -v ./tests/functional/...
```

### As a compiled binary

```sh
go build -o functional-test ./cmd/functional-test/
FTEST_API_URL=http://10.0.0.5:8080 ./functional-test
```

### Via Makefile

```sh
# Runs the compiled binary against whatever is at FTEST_API_URL:
make functional-test
```

## Scenarios covered

| Scenario | Description |
|---|---|
| `health/readiness` | GET /healthz and /readyz return expected payloads |
| `health/root-document` | GET / returns service-name metadata |
| `health/openapi` | GET /openapi.yaml serves a valid spec |
| `health/docs-page` | GET /docs renders HTML |
| `auth/dev-auth-whoami` | Dev-auth Bearer token authenticates /v1/whoami |
| `auth/token-issue-exchange-revoke` | Full token issue → exchange → revoke lifecycle |
| `packages/register` | Register a charm and read it back |
| `revisions/upload-push` | Upload charm archive, push revision, list revisions |
| `releases/channel` | Upload + push + release to channel, verify release list |
| `v2/info-after-release` | V2 info endpoint reflects released data |
| `resources/list` | List declared resources for a charm with resource declarations |
| `resources/revision-lifecycle` | Upload resource file, push revision, list revisions with field validation |
| `resources/download` | Push resource + download via /api/v1/resources/download, verify content integrity |
| `sync/list-rules` | List Charmhub sync rules, verify response shape |
| `sync/add-delete-rule` | Add sync rule → verify in list → delete → verify deletion transition |
| `oci/registry-v2` | OCI registry /v2/ base and /v2/_catalog endpoints reachable |

## Design decisions

- **Endpoint-only**: Every helper targets an externally provided endpoint.
  Service lifecycle (start, restart, persistence) is the responsibility of the
  orchestrator-specific suite (charm or snap), not this package.
- **No test framework dependency in scenarios**: Scenario functions return
  `ScenarioResult` structs rather than calling `t.Fatal()`. This lets both
  the Go test runner and the CLI binary consume them uniformly.
- **Unique names**: Each scenario generates a unique package name (timestamp
  suffix) to avoid cross-scenario collisions.
