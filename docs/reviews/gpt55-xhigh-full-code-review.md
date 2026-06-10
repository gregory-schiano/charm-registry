# GPT 5.5 xhigh full code review — prod-ready-use-harbor/complete

Date: 2026-06-08
Reviewer: Hermes Agent, GPT 5.5 xhigh
Repository: `/home/gschiano/charm-registry`
Head reviewed: `prod-ready-use-harbor/complete` @ `87cec4289830ba14847174d43684fa0143484a6b`
Base used for branch delta: `origin/use-harbor` @ `f910196fef32cea23bb8a8fc1a9bfb5915db3753`
Merge base: `f910196fef32cea23bb8a8fc1a9bfb5915db3753`
Evidence directory: `/tmp/charm-registry-review`

> Note: the local `use-harbor` ref was not present when re-checking the workspace, so this review used the remote-tracking base `origin/use-harbor`. The merge-base equals `origin/use-harbor`, so `origin/use-harbor...HEAD` is the full branch delta requested by the card.

## Executive summary

Verdict: changes requested before this branch is production-ready.

I did not find a critical runtime security flaw in the core auth, permission, OCI, storage, or sync implementation. The product code has several good hardening changes: OIDC/dev-auth separation, bcrypt hashing for newly issued store tokens, SQLC-backed PostgreSQL access, SHA verification on downloads/uploads, SSRF defenses in the Charmhub client, and removal of live Docker/Compose/testcontainer implementation paths.

However, the branch currently fails its own required CI coverage gate and has multiple high-severity CI/release correctness issues that can block or mis-publish production artifacts. There are also medium security/auth robustness issues around rate-limit bypass and token-prefix lookup collisions, plus integration-test gating and cleanup gaps.

Summary of findings:

- Blocking: 1
- High: 4
- Medium: 6
- Low: 4
- Docker/Compose cleanup: live implementation cleanup is complete; stale historical/planning docs still contain Docker/Compose instructions and should be clearly archived or updated.

## Scope reviewed

Review covered the full branch delta against `origin/use-harbor`:

- 115 files changed
- 14,507 insertions
- 960 deletions
- Major areas: `.github/workflows`, `Makefile`, release docs, functional/integration test suites, auth/API middleware, SQLC repository layer, token hashing migrations, OCI/Harbor client, Charmhub sync/reconcile code, snap/charm/rock packaging, Docker/Compose cleanup.

Focused evidence files were written under `/tmp/charm-registry-review/diffs/`:

- `security-auth-storage-api.diff`
- `oci-sync-charmhub.diff`
- `repo-migrations.diff`
- `tests-ci-make.diff`
- `docs-deployment.diff`

## Blocking findings

### B-1. Required CI test gate fails with current coverage

Severity: blocking

References:

- `.github/workflows/ci.yml:71-82`
- `.github/workflows/ci.yml:196-218`
- `/tmp/charm-registry-review/make-coverage.log:641`

Evidence:

- CI runs `make coverage` and then enforces `THRESHOLD=70`.
- Local evidence from the branch reports:
  - `total: (statements) 63.1%`
- Re-running the same threshold expression produced:
  - `coverage=63.1%`
  - `ci-threshold-result=FAIL (63.1% < 70%)`
- The required status gate fans in the `test` job, so this failure propagates to the branch-level gate.

Impact:

- The branch cannot pass its declared required CI checks as written.
- This blocks merge/release workflows that depend on the required-status-checks job.

Suggested remediation:

- Either raise test coverage to at least 70%, or intentionally set a ratcheted threshold to the current accepted baseline and document the ratchet policy.
- If lowering the threshold, use a follow-up task to raise it gradually and make the threshold explicit in docs/testing.md.

## High findings

### H-1. `make coverage` can hide failing package tests

Severity: high

References:

- `Makefile:69-78`
- `.github/workflows/ci.yml:71-82`

Evidence:

`make coverage` loops over packages:

```make
@for pkg in $(_COVER_PKGS); do \
    tmp_cov=$$(mktemp); \
    $(GO) test $$pkg -coverprofile=$$tmp_cov -covermode=count >/dev/null; \
    if [ -s "$$tmp_cov" ]; then tail -n +2 "$$tmp_cov" >> coverage.out; fi; \
    rm -f "$$tmp_cov"; \
done
$(GO) tool cover -func=coverage.out
```

There is no `set -e`, no explicit status capture for `go test`, and the last command in each loop body is `rm -f`. A package test failure can therefore be overwritten by the later successful `rm` and final coverage aggregation.

Impact:

- Once the numeric coverage threshold is satisfied, CI can pass even if one covered package's tests fail.
- This undermines the required test gate for production-readiness.

Suggested remediation:

- Add `set -e` to the loop shell, or explicitly track failures and exit non-zero after the loop.
- Prefer running `make test` before `make coverage` in CI, or use a single fail-fast `go test` invocation where practical.
- Add a small negative test/script check for the Make target so future edits do not reintroduce masked test failures.

### H-2. Release tag push patterns use regex-like syntax in GitHub glob filters

Severity: high

References:

- `.github/workflows/release.yml:3-7`
- `.github/workflows/release.yml:81-87`

Evidence:

The workflow trigger uses:

```yaml
on:
  push:
    tags:
      - "v[0-9]+.[0-9]+.[0-9]+*"
      - "v[0-9]+.[0-9]+.[0-9]+*-rc[0-9]+"
```

GitHub Actions tag filters are glob patterns, not regular expressions. The `+` characters are not regex quantifiers in this context. The workflow later validates semver correctly with `grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+(-rc[0-9]+)?$'`, but normal tags such as `v1.2.3` may never trigger the workflow in the first place.

Impact:

- Normal semver tag pushes may not start the release workflow.
- This can silently break the documented tag-driven release process.

Suggested remediation:

- Use a broad glob such as `v*` for the trigger, then keep the existing regex validation step as the authoritative filter.
- Alternatively, use tested glob patterns that are known to match the supported tag shapes.
- Add actionlint or a workflow trigger smoke test to CI/release dry-runs.

### H-3. Manual release can publish artifacts from the selected workflow ref, not the requested tag

Severity: high

References:

- `.github/workflows/release.yml:14-17`
- `.github/workflows/release.yml:45-75`
- `.github/workflows/release.yml:95`
- `.github/workflows/release.yml:123`
- Additional checkout steps: `.github/workflows/release.yml:166`, `201`, `223`, `248`, `280`, `336`

Evidence:

- `workflow_dispatch` requires a `tag` input and `release-metadata` exposes it as `needs.release-metadata.outputs.tag`.
- Build jobs use pinned `actions/checkout`, but no checkout step sets `ref: ${{ needs.release-metadata.outputs.tag }}`.
- Grep evidence showed checkout steps at lines 95, 123, 166, 201, 223, 248, 280, and 336, all without a tag ref override.

Impact:

- A manual non-dry-run release can label GitHub releases, rocks, charms, snaps, or SBOMs as `vX.Y.Z` while building from the branch/ref selected in the Actions UI.
- This creates provenance and supply-chain risk because artifact content may not match the release tag.

Suggested remediation:

- For manual releases, validate that the requested tag exists and check out that exact tag in every build/publish job.
- Consider failing manual non-dry-run releases unless `github.ref_name == inputs.tag`.
- Include the checked-out commit SHA in the release summary and attestations.

### H-4. Snap publish step masks upload failures and can release a stale revision

Severity: high

References:

- `.github/workflows/release.yml:468-482`
- `.github/workflows/release.yml:484-495`

Evidence:

The snap upload step runs:

```bash
OUTPUT=$(snapcraft upload "$SNAP_FILE" 2>&1) || true
REVISION=$(echo "$OUTPUT" | grep -oP 'Revision \K[0-9]+' || echo "")
if [ -z "$REVISION" ]; then
  REVISION=$(snapcraft list-revisions charm-registry 2>/dev/null | head -1 | awk '{print $1}')
fi
```

The `|| true` suppresses upload failure. If parsing fails, the workflow falls back to the first revision from `list-revisions`, which may be an older uploaded revision unrelated to the current `.snap` file. The later release step releases whatever revision was inferred.

Impact:

- Authentication/network/upload failures can be hidden.
- The release job can promote a stale snap revision to a channel.
- Operators may believe the new build was published when it was not.

Suggested remediation:

- Remove `|| true`; fail immediately on upload errors.
- Parse the revision from a reliable `snapcraft upload` output or API result.
- If parsing fails, stop the workflow rather than falling back to `list-revisions`.
- Optionally verify the revision's metadata/hash matches the just-built artifact before release.

## Medium findings

### M-1. IP rate limiter trusts spoofable `X-Forwarded-For` and can grow unbounded

Severity: medium

References:

- `internal/api/http.go:60`
- `internal/api/http.go:420-467`
- `internal/api/http.go:470-484`
- `internal/api/http.go:487-493`

Evidence:

- The router installs `chimiddleware.RealIP`, which itself trusts forwarded-client headers.
- The custom `rateLimit` middleware then directly replaces `r.RemoteAddr` with the first `X-Forwarded-For` value if present.
- `ipRateLimiter.entries` creates one map entry per key.
- `Cleanup()` exists but is not scheduled by the API construction path.

Impact:

- A direct unauthenticated client can rotate `X-Forwarded-For` values to bypass per-IP throttling.
- The same rotation can create many stale limiter entries, causing memory growth until process restart.
- This weakens protection for unauthenticated and authentication-adjacent endpoints.

Suggested remediation:

- Trust forwarded headers only when the immediate peer is a configured trusted proxy.
- Otherwise key on a parsed/normalized `RemoteAddr` IP address.
- Schedule periodic cleanup or replace the map with a bounded TTL/LRU structure.
- Add tests proving spoofed `X-Forwarded-For` does not bypass limits and stale IP keys are evicted.

### M-2. Bcrypt token lookup uses a short non-unique prefix and verifies only one candidate

Severity: medium

References:

- `internal/auth/auth.go:187-193`
- `internal/auth/auth.go:207-224`
- `internal/auth/auth.go:230-244`
- `internal/repo/queries/accounts.sql:66-81`
- `internal/repo/migrations/0006_token_bcrypt.sql:1-5`
- `internal/repo/sqlite/migrations/0002_token_bcrypt.sql:1-4`

Evidence:

- Newly issued tokens are `cr_` plus 32 random bytes encoded with base64url.
- `TokenPrefixFromRaw` stores only the first 8 characters. Because all new tokens start with `cr_`, this leaves about five random base64url characters in the indexed prefix.
- `FindStoreTokenByPrefix` is a SQLC `:one` query over `WHERE t.token_prefix = $1`.
- The prefix index is non-unique.
- `findAndVerifyToken` verifies only the single returned bcrypt row; on mismatch it falls back to SHA-256 lookup, which cannot discover another bcrypt token with the same prefix.

Impact:

- Prefix collisions can make valid bcrypt tokens intermittently fail authentication, depending on which matching row the database returns.
- Collision likelihood becomes material as token counts grow; an actor able to mint many tokens could intentionally create denial of service for colliding tokens.

Suggested remediation:

- Query all active rows for a prefix and bcrypt-compare each candidate, or store a much longer prefix.
- If keeping one-row lookup, enforce a unique prefix and retry token generation on collision.
- Add regression tests with two bcrypt tokens that intentionally share a prefix.

### M-3. Integration suites are not gated on pull requests or releases

Severity: medium

References:

- `.github/workflows/integration.yml:3-8`
- `.github/workflows/release.yml:90-105`
- `docs/testing.md:119-122`

Evidence:

- The integration workflow is configured for `workflow_dispatch` and scheduled weekly runs.
- Release testing runs `make audit` and `make coverage`, but does not require a charm/Jubilant or snap/spread integration result.
- Local environment check found `charmcraft`, `rockcraft`, `snapcraft`, `spread`, `juju`, `lxc`, and `skopeo` unavailable, so the review could not run those integration suites locally.

Impact:

- Charm relation regressions, snap packaging regressions, and spread test failures can merge or publish before being exercised by release automation.
- Weekly/manual integration is useful, but it is not a production release gate.

Suggested remediation:

- Add at least a smoke subset of integration tests on PRs touching charm/snap/packaging/API behavior.
- For releases, require a successful integration workflow for the exact tag/commit, or make the release workflow depend on integration jobs directly.
- Document any intentionally manual gate in `docs/release.md` with the exact required run URL/status.

### M-4. `snap-integration-test` runs a stray local functional-test after spread

Severity: medium

References:

- `Makefile:171-179`

Evidence:

`make -n snap-integration-test` prints:

```text
if command -v spread >/dev/null 2>&1; then \
	spread -v ./tests/spread/...; \
else \
	echo "ERROR: spread not found — install it (snap install spread --classic) or run in CI."; \
	exit 1; \
fi
FTEST_API_URL=http://localhost:8080 /home/gschiano/charm-registry/.bin/functional-test
```

The target runs the spread suite and then separately runs a local `functional-test` binary against `localhost:8080`. That second command is outside the `spread` VM/backend and is not documented by the target name.

Impact:

- A successful spread run can be followed by an unrelated local-service failure if no local service is running or `.bin/functional-test` is stale/missing.
- This creates false negatives and makes CI/local failures harder to interpret.

Suggested remediation:

- Remove the trailing functional-test command from `snap-integration-test`, or make it a separate explicit target.
- If the local functional test is intentional, add `functional-test-build` as a dependency and document the local service requirement.

### M-5. Spread restore hook typo prevents snap purge

Severity: medium

References:

- `spread.yaml:26-27`

Evidence:

The restore hook uses:

```yaml
restore: |
  snap remove --purse charm-registry || true
```

The valid snap option is `--purge`, not `--purse`.

Impact:

- Snap data/services can leak between spread runs.
- Persistent state can hide failures or create order-dependent failures.

Suggested remediation:

- Change to `snap remove --purge charm-registry || true`.
- Consider shellchecking or smoke-validating spread snippets in CI.

### M-6. PostgreSQL `pg_trgm` migration may require privileges not documented in operator guidance

Severity: medium

References:

- `internal/repo/migrations/0008_search_trgm.sql:1-6`
- `docs/deployment.md`
- `docs/operations.md`

Evidence:

Migration 0008 runs:

```sql
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS idx_packages_name_trgm
    ON packages USING gin (name gin_trgm_ops);
```

The migration is reasonable for package search performance, but creating extensions can require database-owner or elevated privileges depending on the PostgreSQL deployment. The review did not find operator documentation for this privilege requirement or a fallback if the extension cannot be created.

Impact:

- Upgrades can fail at migration time on restricted managed PostgreSQL installations.
- Search performance remediation is tied to an extension creation side effect without a documented preflight.

Suggested remediation:

- Document that the database user must be able to create/use `pg_trgm`, or pre-provision the extension in deployment docs.
- Consider detecting extension creation failure with a clearer operator-facing error.
- If restricted Postgres targets are supported, provide a fallback index/search path or an opt-in migration mode.

## Low findings

### L-1. `/metrics` is exposed without authentication

Severity: low

References:

- `internal/api/http.go:69-76`
- `internal/api/metrics.go:40-42`

Evidence:

`/metrics` is mounted before the authenticated route group and serves `promhttp.Handler()` directly.

Impact:

- Public deployments may expose process/runtime/build metrics and traffic-shape information to unauthenticated users.
- This is not necessarily a bug if metrics are only reachable on a trusted network, but the exposure should be explicit.

Suggested remediation:

- Protect `/metrics` with authentication, bind it to an internal listener, or make exposure configurable.
- Document expected network placement for metrics scraping.

### L-2. OCI provisioning errors can leak backend details to authorized clients

Severity: low

References:

- `internal/service/oci.go:57-68`
- `internal/api/http.go:323-326`

Evidence:

The service wraps OCI provisioning failures with the underlying error text and the API writes service error messages to clients.

Impact:

- Authorized callers may see Harbor/OCI backend implementation details when provisioning fails.
- This can increase diagnostic exposure; it is not a broad unauthenticated leak.

Suggested remediation:

- Keep the detailed cause in logs and structured error causes.
- Return a generic client-facing message such as `OCI package provisioning is unavailable`.

### L-3. Stale Docker/Compose instructions remain in historical/planning docs

Severity: low

References:

- `docs/quality-gate-report-consolidated.md:41-47`
- `docs/quality-gate-report-consolidated.md:81-83`
- `docs/plans/integration-test-suite.md:31-65`
- `docs/plans/ci-release-design.md:164-225`
- `docs/plans/ci-release-design.md:266-279`

Evidence:

Live Docker/Compose implementation files were removed, but several planning/review documents still contain runnable Docker Compose snippets, Dockerfile references, or statements that tests need Docker. Some docs are clearly historical, but not every file is marked as superseded/archive-only at the top.

Impact:

- Operators and future agents can be misled into trying obsolete Docker/Compose flows.
- This partially weakens the no-Docker cleanup story even though live implementation paths are gone.

Suggested remediation:

- Add an explicit superseded banner to historical planning/review docs, or move them under a clearly archived/raw path.
- Remove runnable Docker/Compose instructions from active docs unless they are intentionally retained as historical evidence.

### L-4. Branch diff has whitespace check failures in raw review notes

Severity: low

References:

- `docs/reviews/raw/code-review-glm-5.1.md:3-5`
- `docs/reviews/raw/code-review-glm-5.1.md:40`
- `docs/reviews/raw/code-review-glm-5.1.md:59`

Evidence:

During the post-unblock verification pass, `git diff --check origin/use-harbor...HEAD` returned exit 2 with trailing whitespace in newly added lines in `docs/reviews/raw/code-review-glm-5.1.md`.

Impact:

- If whitespace checks are added to CI or pre-commit gates, the branch will fail until these raw notes are cleaned.
- The issue is documentation-only and does not affect runtime product behavior, but it adds avoidable quality noise to the review artifact set.

Suggested remediation:

- Strip trailing whitespace from the referenced raw review note lines.
- Consider adding a lightweight whitespace check to a documentation lint target if the repository wants to keep raw review artifacts in the branch delta.

## Non-blocking recommendations

1. Add actionlint to CI or a local verification target for workflow syntax and trigger validation.
2. Add regression tests for the IP rate limiter's forwarded-header trust behavior and cleanup behavior.
3. Add regression tests for bcrypt token-prefix collision handling.
4. Add a release dry-run checklist that records the checked-out commit SHA for every artifact-producing job.
5. Make the integration test gating policy explicit: which jobs are required for PR, tag, release, and manual operator acceptance.
6. Add a short migration preflight section to docs/operations.md for PostgreSQL extensions, advisory locks, and migration failure recovery.
7. Clean trailing whitespace in raw review notes before merge if the repository intends to enforce diff whitespace checks.

## Positive observations

- `make test`, `make vet`, `make lint`, `make sqlc-diff`, `make build`, `make gosec`, `make vuln`, and `make test-race` all passed in the collected evidence.
- `govulncheck` reported no called vulnerabilities in project code.
- Race detector passed across internal packages.
- SQLC drift check passed.
- Live Docker/Compose/testcontainers implementation paths were removed.
- Auth routes are consistently wrapped with identity middleware for protected API groups.
- Newly issued store tokens use bcrypt rather than raw SHA-256 hashes.
- Repository robot credential conversion now fails loudly on partial/corrupt rows instead of silently dropping data.
- Charmhub client includes SSRF-oriented URL validation and response size limits.
- The branch adds broad functional/integration test scaffolding and release/operator documentation, even though not all integration paths are currently gated.

## Docker/Compose/testcontainers cleanup assessment

Status: live cleanup complete; documentation cleanup incomplete.

Evidence for live cleanup:

- Deleted in branch delta:
  - `.dockerignore`
  - `Dockerfile`
  - `compose.yaml`
- `git ls-files | grep -Ei '(^|/)(Dockerfile|compose.*\.ya?ml|\.dockerignore)$'` returned no live files.
- Grep of `.github/workflows/*.yml` and `Makefile` found no active `docker build`, `docker compose`, `docker/login-action`, `docker/metadata-action`, or `docker/build-push-action` usage.
- No `testcontainers-go` dependency remains in `go.mod`.

Allowed legitimate OCI terminology that remains:

- `docker://` skopeo transport examples/usages in release/deployment docs and release workflow.
- `/docker/registry/v2/repositories` in `internal/oci/client.go:355`, which is the Docker Registry HTTP API V2 storage path format used by OCI-compatible registries, not Docker daemon usage.
- Indirect `github.com/docker/*` modules in `go.mod`/`go.sum` via OCI/registry tooling dependencies, not direct Docker daemon/testcontainers usage.

Remaining cleanup gap:

- Several planning/review docs still include Docker/Compose instructions. These are acceptable only if clearly marked as historical/superseded; otherwise they should be updated or archived.

## Commands run and real output summaries

### Workspace and branch verification

Command:

```bash
git status --short
git branch --show-current
git rev-parse HEAD
git rev-parse origin/use-harbor
git merge-base origin/use-harbor HEAD
```

Output summary:

```text
git status --short: clean
git branch --show-current: prod-ready-use-harbor/complete
git rev-parse HEAD: 87cec4289830ba14847174d43684fa0143484a6b
git rev-parse origin/use-harbor: f910196fef32cea23bb8a8fc1a9bfb5915db3753
git merge-base origin/use-harbor HEAD: f910196fef32cea23bb8a8fc1a9bfb5915db3753
```

### Branch delta

Command:

```bash
git diff --stat origin/use-harbor...HEAD
git diff --name-only origin/use-harbor...HEAD
```

Output summary:

```text
115 files changed, 14507 insertions(+), 960 deletions(-)
```

The full stat and file list are saved in:

- `/tmp/charm-registry-review/diff-stat.log`
- `/tmp/charm-registry-review/diff-files.log`

### Post-unblock verification refresh

Commands re-run on 2026-06-09T08:34:58+02:00 before completing the Kanban card:

```bash
git status --short
git branch --show-current
git rev-parse HEAD
git rev-parse origin/use-harbor
git diff --stat origin/use-harbor...HEAD
git diff --name-only origin/use-harbor...HEAD
git diff --check origin/use-harbor...HEAD
make test
make vet
make lint
make sqlc-diff
```

Output summary:

```text
git status --short: ?? docs/reviews/gpt55-xhigh-full-code-review.md
git branch --show-current: prod-ready-use-harbor/complete
git rev-parse HEAD: 87cec4289830ba14847174d43684fa0143484a6b
git rev-parse origin/use-harbor: f910196fef32cea23bb8a8fc1a9bfb5915db3753
git diff --stat origin/use-harbor...HEAD: 115 files changed, 14507 insertions(+), 960 deletions(-)
git diff --name-only origin/use-harbor...HEAD: 115 files
git diff --check origin/use-harbor...HEAD: exit 2; trailing whitespace in docs/reviews/raw/code-review-glm-5.1.md lines 3, 4, 5, 40, and 59
make test: exit 0; internal Go package tests passed
make vet: exit 0
make lint: exit 0; golangci-lint reported 0 issues
make sqlc-diff: exit 0; no generated-code drift reported
```

### Unit tests

Command:

```bash
make test
```

Output summary:

```text
go list ./internal/... | grep -Ev '/repo/db$' | xargs go test
ok github.com/gschiano/charm-registry/internal/api
ok github.com/gschiano/charm-registry/internal/app
ok github.com/gschiano/charm-registry/internal/auth
ok github.com/gschiano/charm-registry/internal/blob
ok github.com/gschiano/charm-registry/internal/charm
ok github.com/gschiano/charm-registry/internal/charmhub
ok github.com/gschiano/charm-registry/internal/config
ok github.com/gschiano/charm-registry/internal/oci
ok github.com/gschiano/charm-registry/internal/repo
ok github.com/gschiano/charm-registry/internal/service
ok github.com/gschiano/charm-registry/internal/sync
```

Full log: `/tmp/charm-registry-review/make-test.log`

### Vet, lint, SQLC drift

Commands:

```bash
make vet
make lint
make sqlc-diff
```

Output summary:

- `make vet`: exit 0
- `make lint`: exit 0
- `make sqlc-diff`: exit 0 / no generated-code drift reported

Full log: `/tmp/charm-registry-review/make-vet-lint-sqlc.log`

### Build, gosec, govulncheck

Commands:

```bash
make build
make gosec
make vuln
```

Output summary:

- `make build`: built `.bin/charm-registry` and `.bin/charm-registryctl`
- `make gosec`: completed cleanly with no blocking findings
- `make vuln`: `No vulnerabilities found. Your code is affected by 0 vulnerabilities.`
- `govulncheck` also reported imported/required module vulnerabilities that the project code does not appear to call.

Full log: `/tmp/charm-registry-review/make-build-gosec-vuln.log`

### Coverage

Command:

```bash
make coverage
```

Output summary:

```text
total: (statements) 63.1%
```

CI threshold check replay:

```text
coverage=63.1%
ci-threshold-result=FAIL (63.1% < 70%)
```

Full log: `/tmp/charm-registry-review/make-coverage.log`

### Race detector

Command:

```bash
make test-race
```

Output summary:

- All internal packages passed under `go test -race`.
- No data races reported.

Full log: `/tmp/charm-registry-review/make-test-race.log`

### Integration and packaging tool availability

Command:

```bash
for tool in charmcraft rockcraft snapcraft spread juju lxc skopeo actionlint; do command -v "$tool"; done
```

Output summary:

- No paths were returned for `charmcraft`, `rockcraft`, `snapcraft`, `spread`, `juju`, `lxc`, `skopeo`, or `actionlint` in the local review environment.
- Therefore charm/Jubilant, snap/spread, rock/skopeo, and workflow lint checks were not executed locally during this review.

### Docker/Compose cleanup search

Commands:

```bash
grep -nEi 'docker|compose|testcontainers|build-push-action|metadata-action|login-action' .github/workflows/*.yml Makefile || true
git ls-files | grep -Ei '(^|/)(Dockerfile|compose.*\.ya?ml|\.dockerignore)$' || true
```

Output summary:

- No live Dockerfile/Compose/.dockerignore files remain tracked.
- Workflow/Makefile matches were limited to legitimate `docker://` skopeo transport references in release workflow.
- Broader repository search still finds historical/planning doc references, listed in L-3.

## Review limitations

- The local review environment did not have `juju`, `lxc`, `charmcraft`, `rockcraft`, `snapcraft`, `spread`, `skopeo`, or `actionlint`, so deployment/integration workflows were reviewed statically and from workflow/test source rather than executed end-to-end.
- One focused sub-review for OCI/storage exhausted its tool/model budget before returning a usable summary. I still reviewed OCI, sync, repository migrations, and storage code directly and included the findings above.
- This task was review/report-only. No product code was modified.
