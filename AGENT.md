# Agent Guide

## Project Explanation

This repository implements a private Charm Registry in Go.

Its job is to look enough like Charmhub and the Charmcraft publisher APIs that stock `juju` and stock `charmcraft` can talk to it without custom patches.

At a high level:

- Charm and resource metadata lives in Postgres.
- File artifacts live in S3-compatible object storage.
- OCI image resources are stored in the embedded OCI Distribution registry by default.
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
- `internal/oci`
  - Embedded OCI Distribution registry backend and package-scoped push/pull auth.
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

- Local development runs the binary directly — no container orchestration needed.
  - When adding a new config env var, make sure it is documented in `.env.example` and the README.
- `go test ./...` is not the preferred repo-wide test command here.
  - Use the `Makefile` targets or `./cmd/... ./internal/...` package scope instead.
- Embedded OCI is the default local registry path.

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
  4. tested against the running binary or snap/charm

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

- `make actionlint`
- `make tidy-check`
- `make vet`
- `make lint`
- `make sqlc-diff`
- `make vuln`
- `make gosec`
- `make test`
- `make coverage`
- coverage threshold: `65%` (ratchet policy — see `docs/coverage-policy.md`)
- `make build`

### Practical notes

- If you touch `internal/repo/queries/*.sql`, run `make sqlc-diff`.
- If you touch wire compatibility in `internal/api` or `internal/service`, run the relevant API and service tests, not just package-local unit tests.
- If you add config, test it against a running instance to verify the env var is actually read.

## DO And DON'T

### DO

- Do preserve client compatibility with `juju` and `charmcraft`.
- Do prefer small, surgical changes over adjacent cleanup.
- Do keep Postgres behavior and sqlc query files in sync.
- Do add regression tests when fixing a bug.
- Do check both code and deployment wiring when adding config.
- Do document new env vars in `.env.example` and `README.md` when they are user-facing.
- Do use typed structs when they materially improve safety in large response builders.
- Do propagate errors instead of swallowing them.
- Do use transactions for multi-step mutations that must succeed or fail together.
- Do verify behavior with the real Postgres-backed path when changing SQL or ACL logic.
- Do keep admin-only and sync-managed flows explicit and conflict clearly when they block normal publisher operations.

### DON'T

- Don’t hand-edit sqlc-generated files under `internal/repo/db`.
- Don't assume a new env var works in production just because it exists in `.env` — verify the running binary reads it.
- Don’t use `go test ./...` as the default repo-wide check here.
- Don’t silently fall back on invalid config values.
- Don’t hide service or repo failures behind empty responses or zero values.
- Don’t reintroduce N+1 query patterns in service workflows when batch loading is available.
- Don’t make read/download paths depend on hidden OCI repair/provisioning side effects.
- Don’t refactor public response shapes casually.
- Don’t remove “unused” code in this repo without checking whether it is part of compatibility, test scaffolding, or generated workflow.
- Don’t bypass the service layer for business workflows that need auth, transactions, or invariants.

## Avoiding AI Sloppiness

This section records concrete antipatterns that appeared in this codebase during the initial AI-assisted authoring and were cleaned up during a deep review. The goal is to keep future agent contributions from regenerating the same noise.

### Tests

- Don't narrate the test.
  - No `// Arrange`, `// Act`, `// Assert` block headers.
  - No comments that restate the assertion immediately below them.
  - The test name and the code itself are the documentation.

- Don't enumerate every interface method just to inflate coverage.
  - One representative test per error kind beats a 15-row table that calls every method and asserts the same `ErrNotFound` on each.
  - If every method genuinely needs to be exercised, generate the cases via reflection rather than hand-writing them.
  - See `TestMemoryRepresentativeMethodsReturnNotFoundForMissingKeys` for the right shape.

- Don't property-dump structs.
  - Avoid long sequences of `assert.Equal(t, expected, cfg.Field)` over every field.
  - Compare against an expected struct literal in one assertion, or assert only the fields whose parsing is non-trivial.
  - See `TestLoadDefaults` and `configSnapshot` for the right shape.

- Don't write fakes that mirror the production implementation 1:1.
  - A fake that returns `"upstream-" + name` for any input is a trampoline; tests using it pass even when the integration is wrong.
  - Prefer the real component (in-memory repo, `httptest.Server`) or a fake that captures intent the real one cannot — forced failures, deterministic IDs, recorded calls.

- Don't assert on raw error message strings.
  - `assert.Contains(t, err.Error(), "package not found")` couples the test to wording.
  - Use `errors.Is`, `errors.As`, or compare `service.Error.Kind` / `Code`.

- Don't call `time.Now()` inside tests when ordering or equality matters.
  - Inject a clock. `Service.Clock` and `sync.Service.Clock` exist for this.
  - In repo tests where ordering matters, use `time.Unix(int64(i), 0).UTC()` so values are deterministic.

- Don't use placeholder fixtures like `"foo"`, `"bar"`, `"test1"`.
  - Use realistic shapes: `"postgresql-k8s"`, `"alice@example.com"`, real-ish hash widths.
  - Bugs around length, allowed characters, and Unicode only surface against realistic data.

- Don't write hollow fuzz tests.
  - Round-trip fuzz (`parse(format(x)) == x`) only proves invertibility.
  - Targets should assert real invariants: no panic, parsed values inside expected ranges, no path escape, parses-or-errors-cleanly.

- Don't add `t.Parallel()` to a test that calls `t.Setenv`.
  - `Setenv` is incompatible with parallel tests or parallel ancestors and will panic at runtime.
  - This is not a Go-version thing; it has always been the contract.

- Do cover the things that actually matter, even when they're harder.
  - Transaction rollback under partial failure — see `TestPostgresPushRevisionStyleTransactionRollsBackOnUpdatePackageFailure`.
  - Concurrent writers on the in-memory repo.
  - Upload-size boundaries (`MaxUploadBytes`, `±1`).
  - Auth-required routes returning `401` for missing or invalid tokens.

### Code

- Don't nil-check collaborators that are wired in `New` and never set to nil afterwards.
  - `if s.oci == nil { ... }` inside service methods is dead code when `Service.New` always assigns it.
  - Tests should inject a no-op or fake, never `nil`.

- Don't write wrapper methods that only forward to the repo.
  - A service method that takes an identity, calls `s.repo.X`, and returns its result earns its place only if it adds an auth check, a transaction, a transformation, or an invariant.

- Don't comment what the code already says.
  - Skip `// CreatePackage creates a package`.
  - Skip `// is part of the [Repository] interface` — the compiler enforces it.
  - Comments earn their place by explaining a non-obvious decision or workflow.

- Don't introduce magic numbers; name them.
  - Token TTLs, poll intervals, body-size limits, default ports — all should be named constants in the package or in `config.Config`.

- Don't silently swallow errors with `_ = …`.
  - Either handle the error (return, log, retry) or document explicitly why ignoring it is correct.
  - Especially in cleanup paths: a failed `ApproveUpload` after a failed archive parse leaves state inconsistent.

- Don't add belt-and-braces validation that masks corrupt data.
  - `if id == nil || *id == 0 || username == "" || secret == "" { return nil }` silently drops partial rows.
  - Distinguish "absent" (`(nil, nil)`) from "corrupt" (`(nil, error)`); fail loudly on corrupt. See the post-review `robotFromSQLC` for the right shape.

- Don't duplicate string literals across handlers and helpers.
  - Common error messages (`"package not found"`, `"upload not found"`) live as `const` in `internal/service/errors.go`.
  - URL-construction helpers (`charmDownloadURL`, resource-download URLs) belong in one place.

- Don't `io.ReadAll` an HTTP response body without a size cap.
  - Wrap with `io.LimitReader`, or use a configured `Max*Bytes` limit. See `charmhub.NewWithLimits` and `oci.checkManifestDescriptor`.

- Don't buffer large artifacts when the contract is streaming.
  - Charm and resource downloads thread `io.ReadCloser` through the service into the handler and use `io.Copy`. The `[]byte` shortcut OOMs under modest concurrency.
  - Uploads: `io.TeeReader` into hashers and `blob.Put` rather than `io.ReadAll` followed by `Put`.

- Don't paper over complexity with `//nolint:gocognit,cyclop,nestif`.
  - The lint disable flags a smell, not a fix. Factor the function or document a real reason it must stay monolithic.

- Don't repeat handler boilerplate.
  - Auth extraction, body-size limiting, identity propagation belong in middleware, not in every handler.
  - The pattern is in place (`requireIdentity`); use it. The single anonymous route is grouped explicitly so the absence of auth is visible at the routing site.

- Don't return `200 OK` from a POST that creates a resource.
  - Use `201 Created` with a `Location` header for synchronous creates.
  - Use `202 Accepted` for async work (the charmhub-sync admin endpoints already do this).

### Architecture

- Don't grow god interfaces.
  - `Repository` was 50+ methods; it is now split into `HealthRepo`, `AccountRepo`, `PackageRepo`, `CharmhubSyncRepo`, with `CompositeRepo` and `Backend` for code that genuinely needs all of it.
  - New repo methods belong in the smallest interface that covers the aggregate. Resist adding to `Backend` directly.

- Don't grow god services.
  - The Charmhub sync flow lives in `internal/sync`, not `internal/service`.
  - When a feature has its own state machine, background loop, or system actor, give it its own package.

- Don't fake authorization with synthetic accounts.
  - Use `core.Identity.System = true`; the auth helpers (`requireAuth`, `requirePermission`, `requirePackageView`, `requirePackageManage`) short-circuit on it explicitly.

- Domain types in `internal/core` should validate themselves.
  - Use `core.NewPackage`, `core.NewRevision`, `core.NewRelease`, `core.NewStoreToken` rather than constructing the struct directly.
  - New domain types should ship with a constructor that enforces required fields.

### Reviewer self-check before sending changes

Run through this list before posting a diff:

- Did I add `t.Parallel()` to a test that calls `t.Setenv`? (It will panic.)
- Did I add a comment that restates what the next line of code does?
- Did I assert on an error message string instead of `errors.Is` or error kind?
- Did I add an `if x == nil` for an injected dependency that is set in `New`?
- Did I add an `_ = something` that swallows an error?
- Did I add a `//nolint` to make a CI rule pass without addressing what it flagged?
- Did I add `io.ReadAll` on an HTTP response body without a size cap?
- Did I add a method to `Backend` without considering which sub-interface it belongs to?
- Did I add a POST that returns `200 OK` instead of `201 Created` for a created resource?
- Did I construct a `core.Package` / `core.Revision` / `core.Release` directly instead of using its `New*` constructor?

If any of these are yes, fix it before sending the change.

## Good First Questions To Ask Before Changing Code

- Is this a compatibility surface for `juju` or `charmcraft`?
- Is this behavior enforced by tests today?
- If I add a config variable, did I also update docs and `.env.example`?
- If I changed SQL, did I update query files and regenerate sqlc output?
- If I changed a multi-step mutation, should this be transactional?
- If I changed a read path, am I accidentally introducing a control-plane side effect?
