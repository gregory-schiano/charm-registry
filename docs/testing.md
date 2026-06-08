# Testing

The registry uses a layered testing approach: unit tests, race-detector tests, and security audits. Integration tests are not yet implemented.

## Unit tests

```bash
make test
```

This runs `go test` on all internal packages, excluding the sqlc-generated `internal/repo/db` package. Tests use real Go testing, table-driven tests, and the standard `testing` package.

### With race detector

```bash
make test-race
```

Useful for catching concurrent access bugs. Runs slower than plain `make test`.

### Coverage

```bash
make coverage
```

Produces a coverage report across all testable packages. Coverage is output via `go tool cover -func=coverage.out`.

## Static analysis

### Vet

```bash
make vet
```

Runs `go vet` on all packages.

### Lint

```bash
make lint
```

Runs `golangci-lint` with the curated rule set in `.golangci.yml`. The configuration is inspired by Juju's Go linting setup.

### Vulnerability scan

```bash
make vuln
```

Runs `govulncheck` on all packages. This scans for known vulnerabilities in dependencies and the Go standard library.

### Security scan

```bash
make gosec
```

Runs `gosec` on all packages, excluding `internal/repo/db` (sqlc-generated SQL string constants trigger false-positive G101 findings).

### Full audit

```bash
make audit
```

Runs `tidy`, `vet`, `lint`, `test`, `vuln`, and `gosec` in sequence.

### Module hygiene

```bash
make tidy-check
```

Verifies that `go.mod` and `go.sum` are already tidy. Useful in CI to catch uncommitted dependency changes.

### sqlc diff

```bash
make sqlc-diff
```

Verifies that sqlc-generated code in `internal/repo/db` matches what `sqlc generate` would produce. Catches drift between SQL queries and generated Go code.

## Integration tests

There is no dedicated integration test suite yet. The production-readiness roadmap includes designing and implementing integration tests covering:

1. Full stack boot (Postgres, MinIO, OCI registry, service)
2. Health and readiness endpoints
3. Token lifecycle (issue, exchange, revoke)
4. Package registration and metadata update
5. Charm upload, revision, release, refresh, find, info
6. Resource upload/download
7. ACL and private package access
8. Body and header limit enforcement
9. Restart persistence (data survives service restart)
10. charmcraft/juju compatibility smoke tests

Integration tests will live under `tests/integration/` or `internal/integration/` once implemented.

## Fuzz tests

The registry includes fuzz tests for security-sensitive parsing:

- `internal/charm/archive_fuzz_test.go` — fuzzing the charm archive parser
- `internal/auth/macaroon_fuzz_test.go` — fuzzing the macaroon token parser
- `internal/api/http_fuzz_test.go` — fuzzing HTTP request handling

Run them with:

```bash
go test -fuzz=FuzzParseArchive ./internal/charm/
go test -fuzz=FuzzParseMacaroon ./internal/auth/
```

## Test isolation

Unit tests use the in-memory repository (`internal/repo/memory.go`) to avoid requiring a live database. Postgres-specific tests use a helper that connects to a real Postgres instance (set `CHARM_REGISTRY_DATABASE_URL` to run them).
