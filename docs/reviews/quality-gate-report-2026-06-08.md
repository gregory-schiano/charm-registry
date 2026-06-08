# Quality Gate Report — prod-ready-use-harbor/complete

Date: 2026-06-08
Branch under test: `prod-ready-use-harbor/complete`
Tip before report commit: `d088656`
Base: `origin/use-harbor` (`f910196`)

## Commands run

### `make check`

Result: **failed** at `make vuln` / `go tool govulncheck`.

Earlier stages inside `make check` succeeded before the failure:

- `go fmt ./cmd/... ./internal/...`
- `go mod tidy`
- `go mod verify` → `all modules verified`
- `go vet ./cmd/... ./internal/...`
- `go tool golangci-lint run ./cmd/... ./internal/...` → `0 issues.`
- unit tests for internal packages → passed

Failure summary from govulncheck:

- `GO-2026-5039` in `net/textproto`, found in stdlib `go1.26.2`, fixed in `go1.26.4`
- `GO-2026-5037` in `crypto/x509`, found in stdlib `go1.26.2`, fixed in `go1.26.4`
- `GO-2026-4982` in `html/template`, found in stdlib `go1.26.2`, fixed in `go1.26.3`
- `GO-2026-4980` in `html/template`, found in stdlib `go1.26.2`, fixed in `go1.26.3`
- `GO-2026-4971` in `net`, found in stdlib `go1.26.2`, fixed in `go1.26.3`
- `GO-2026-4918` in `net/http`, found in stdlib `go1.26.2`, fixed in `go1.26.3`

Observed module/toolchain state:

```text
go version go1.26.2 linux/amd64
go.mod:
  go 1.25.0
  toolchain go1.26.2
```

Root-cause hypothesis: the branch pins/uses Go toolchain `1.26.2`, while govulncheck reports called vulnerable standard-library symbols fixed in Go `1.26.4`.

### `make gosec`

Result: **passed**.

Summary:

```text
Issues: 0
```

### `make build`

Result: **passed**.

Built:

- `.bin/charm-registry`
- `.bin/charm-registryctl`

### `go test ./...`

Result: **passed**.

Includes:

- `cmd/charm-registryctl`
- all internal package tests
- generated db package has no test files

### `make test-race`

Result: **passed**.

Internal package tests passed with race detector.

### `make coverage`

Result: **passed**.

Overall coverage:

```text
total: (statements) 65.3%
```

### `make sqlc-diff`

Result: **failed**.

`go tool sqlc diff` reports generated code drift in:

- `internal/repo/db/models.go`
- `internal/repo/db/querier.go`
- `internal/repo/db/revisions.sql.go`

Notable generated diffs:

- `Upload` model would gain `CreatedByAccountID *string`
- `Querier` would gain `DeleteStaleUploads(ctx context.Context, dollar_1 pgtype.Interval) (int64, error)`
- `GetUpload` return type would change from `Upload` to generated `GetUploadRow`

Root-cause hypothesis: SQL migrations/queries and committed sqlc generated files are out of sync.

### `make integration-test`

Result: **failed before tests ran**.

Failure command path:

- `docker compose -f compose.integration.yaml up --build -d` built image and started dependencies
- `bash ./scripts/wait-for-healthy.sh http://localhost:18080/healthz 120` timed out

Container log excerpt:

```text
time=2026-06-08T12:56:15.354Z level=WARN msg="INSECURE DEV AUTH ENABLED — not for production use"
time=2026-06-08T12:56:15.575Z level=ERROR msg="build application" error="cannot create OCI registry client: cannot read OCI TLS certificate for internal registry client: open /certs/oci.crt: no such file or directory"
```

Observed integration compose wiring:

- `charm-registry-itest` sets:
  - `CHARM_REGISTRY_OCI_TLS_CERT_FILE=/certs/oci.crt`
  - `CHARM_REGISTRY_OCI_TLS_KEY_FILE=/certs/oci.key`
- mounts `./certs:/certs:ro`
- `integration-up` does **not** run `make generate-cert` or otherwise create `certs/oci.crt` and `certs/oci.key`

Root-cause hypothesis: the integration test stack requires OCI TLS certs, but the integration Makefile target/compose stack does not generate them before starting the read-only app container.

## Cleanup

After the integration failure, the stack was cleaned up with:

```bash
docker compose -f compose.integration.yaml down -v
```

Result: containers, volumes, and network removed.

## Fix tasks planned

1. Upgrade/pin the Go toolchain to a govulncheck-clean version (`go1.26.4` or newer), then rerun `make vuln` and `make check`.
2. Regenerate or reconcile sqlc output, then rerun `make sqlc-diff`, compile, and affected repo tests.
3. Fix integration test stack cert bootstrap, then rerun `make integration-test` end-to-end.
