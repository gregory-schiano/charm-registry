# Agent Guide

## Project Explanation

This repository implements a private Charm Registry in Go.

Its job is to look enough like Charmhub and the Charmcraft publisher APIs that stock `juju` and stock `charmcraft` can talk to it without custom patches.

At a high level:

- Charm and resource metadata lives in Postgres.
- File artifacts live in S3-compatible object storage.
- OCI image resources are stored in Harbor.
- The service exposes both publisher-style endpoints (`/v1/...`) and Juju/Charmhub-compatible consumer endpoints (`/v2/...` and `/api/v1/...`).
- The registry can also mirror selected public Charmhub tracks into the local registry with a background sync worker.

This is a compatibility-driven project. Small response-shape changes can break real clients. Treat wire compatibility as a product requirement, not an implementation detail.

## Architecture And Structure

### Main binaries

- `cmd/charm-registry`
  - Main server binary.
- `cmd/charm-registryctl`
  - Admin CLI for managing Charmhub synchronization rules.

### Core packages

- `internal/api`
  - HTTP routing, auth wiring, request/response handling, admin endpoints, compatibility routes.
- `internal/service`
  - Business logic.
  - This is the main orchestration layer for package registration, revisions, resources, releases, OCI helpers, and Charmhub sync.
- `internal/repo`
  - Data access layer.
  - Postgres is the real implementation.
  - The Postgres repo is sqlc-backed for query execution.
  - The memory repo exists mainly for tests and is not the behavioral source of truth.
- `internal/repo/queries`
  - Authoritative SQL query definitions for sqlc-managed repository methods.
- `internal/repo/db`
  - sqlc-generated code.
  - Do not hand-edit these files.
- `internal/blob`
  - S3-compatible blob storage for charm and file-resource payloads.
- `internal/harbor`
  - Harbor control-plane and OCI mirror interactions.
- `internal/charm`
  - Charm archive parsing and safety checks.
- `internal/charmhub`
  - Read-only upstream Charmhub client used by synchronization.
- `internal/auth`
  - OIDC and dev/store-token auth logic.
- `internal/config`
  - Environment-backed runtime configuration.
- `internal/core`
  - Domain types shared across layers.
- `internal/testutil`
  - Test helpers such as local OCI registry fixtures.
- `internal/app`
  - Application wiring and lifecycle.

### Important architectural rules

- API layer maps service/domain errors to HTTP.
  - Do not push HTTP status concerns down into the repo layer.
- Service layer owns workflows and orchestration.
  - Multi-step mutations belong here, usually behind repository transactions.
- Repository layer owns persistence and SQL.
  - If you add or change Postgres behavior, prefer editing `internal/repo/queries/*.sql` and regenerating sqlc output.
- Charmhub sync is registry-owned.
  - The CLI manages rules.
  - The registry process owns scheduling and reconciliation.

### Operational constraints that matter during development

- `docker compose` uses an explicit environment allowlist in `compose.yaml`.
  - Adding a new config env var in `internal/config/config.go` is not enough.
  - If the dev stack should see it, update `compose.yaml` too.
- `go test ./...` is not the preferred repo-wide test command here.
  - The repo includes vendored Harbor content under `deploy/` that can make broad package discovery noisy or unreliable.
  - Use the `Makefile` targets or `./cmd/... ./internal/...` package scope instead.
- Harbor is a real external dependency in the local stack.
  - Some flows depend on valid TLS/certs and correct `CHARM_REGISTRY_HARBOR_*` URLs.

## Code Style And Conventions

### General style

- Keep the code boring and explicit.
- Prefer small, direct functions over abstract helper layers unless the reuse is obvious.
- Match the existing package boundaries instead of inventing new ones.
- Add comments only when they explain a non-obvious decision or workflow.

### SQL and repository conventions

- Postgres repository code should be sqlc-backed.
- Query changes belong in:
  - `internal/repo/queries/*.sql`
- Generated output belongs in:
  - `internal/repo/db/*.go`
- Repository adapters and conversions belong in:
  - `internal/repo/postgres_sqlc.go`
- After changing query files, regenerate and verify sqlc output.
- Do not hand-edit generated files.

### HTTP and compatibility conventions

- Be conservative with public response shapes.
- `juju` and `charmcraft` compatibility is sensitive to:
  - endpoint presence
  - field names
  - optional field semantics
  - status codes
- For Charmhub-compatible endpoints, returning a stable superset is usually safer than trimming fields aggressively unless the API already supports field filtering correctly.

### Config conventions

- Runtime config is loaded through `internal/config/config.go`.
- New env vars should usually be:
  1. parsed and validated in config
  2. documented in `.env.example`
  3. documented in `README.md` if user-facing
  4. forwarded in `compose.yaml` if the local Compose stack needs them

### Testing conventions

- Prefer test-first for non-trivial logic.
- Service behavior should usually be covered in `internal/service/*_test.go`.
- SQL behavior should be covered with Postgres-backed tests where it matters.
- Use the memory repo for fast unit tests, but do not trust it as proof that Postgres behavior is correct.

### Security and safety conventions

- Avoid panics in library/service/repo code paths.
- Fail loudly on invalid configuration rather than silently falling back.
- Keep archive parsing bounded.
- Keep HTTP timeouts and body-size limits intact unless there is a deliberate reason to change them.

### Fast local checks

- Format code:
  - `make fmt`
- Verify modules:
  - `make tidy`
- Run unit tests:
  - `make test`
- Run race detector:
  - `make test-race`

### CI-aligned checks

- Run vet:
  - `make vet`
- Run lint:
  - `make lint`
- Run vulnerability scan:
  - `make vuln`
- Run static security scan:
  - `make gosec`
- Verify sqlc output is current:
  - `make sqlc-diff`
- Run coverage:
  - `make coverage`
- Run the usual pre-merge bundle:
  - `make audit`

### What CI currently enforces

From `.github/workflows/ci.yml`:

- `make tidy-check`
- `make vet`
- `make lint`
- `make sqlc-diff`
- `make vuln`
- `make gosec`
- `make coverage`
- coverage threshold: `70%`
- `make build`

### Practical notes

- If you touch `internal/repo/queries/*.sql`, run `make sqlc-diff`.
- If you touch wire compatibility in `internal/api` or `internal/service`, run the relevant API and service tests, not just package-local unit tests.
- If you add config, render Compose config to verify it actually reaches the container:
  - `docker compose config`

## DO And DON'T

### DO

- Do preserve client compatibility with `juju` and `charmcraft`.
- Do prefer small, surgical changes over adjacent cleanup.
- Do keep Postgres behavior and sqlc query files in sync.
- Do add regression tests when fixing a bug.
- Do check both code and deployment wiring when adding config.
- Do treat `compose.yaml` as part of the runtime contract for local development.
- Do use typed structs when they materially improve safety in large response builders.
- Do propagate errors instead of swallowing them.
- Do use transactions for multi-step mutations that must succeed or fail together.
- Do verify behavior with the real Postgres-backed path when changing SQL or ACL logic.
- Do keep admin-only and sync-managed flows explicit and conflict clearly when they block normal publisher operations.

### DON'T

- Don’t hand-edit sqlc-generated files under `internal/repo/db`.
- Don’t assume a new env var works in Docker Compose just because it exists in `.env`.
- Don’t use `go test ./...` as the default repo-wide check here.
- Don’t silently fall back on invalid config values.
- Don’t hide service or repo failures behind empty responses or zero values.
- Don’t reintroduce N+1 query patterns in service workflows when batch loading is available.
- Don’t make read/download paths depend on hidden Harbor repair/provisioning side effects.
- Don’t refactor public response shapes casually.
- Don’t remove “unused” code in this repo without checking whether it is part of compatibility, test scaffolding, or generated workflow.
- Don’t bypass the service layer for business workflows that need auth, transactions, or invariants.

## Good First Questions To Ask Before Changing Code

- Is this a compatibility surface for `juju` or `charmcraft`?
- Is this behavior enforced by tests today?
- If I add a config variable, did I also update docs and Compose wiring?
- If I changed SQL, did I update query files and regenerate sqlc output?
- If I changed a multi-step mutation, should this be transactional?
- If I changed a read path, am I accidentally introducing a control-plane side effect?
