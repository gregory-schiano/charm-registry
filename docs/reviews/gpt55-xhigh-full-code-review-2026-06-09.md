# Full Code Review — `prod-ready-use-harbor/complete`

- **Reviewer profile:** gpt55xhigh (GPT 5.5, xhigh reasoning)
- **Date:** 2026-06-09
- **Repository:** `/home/gschiano/charm-registry`
- **Branch:** `prod-ready-use-harbor/complete`
- **HEAD reviewed:** `87cec4289830ba14847174d43684fa0143484a6b`
- **Base for delta:** `origin/use-harbor` (merge-base `f910196fef32cea23bb8a8fc1a9bfb5915db3753`, equal to `origin/use-harbor`, so `origin/use-harbor...HEAD` is the full branch delta)
- **Scope:** full branch delta — 115 files, +14,507 / −960

> This report is an independent verification pass written to a fresh file. A prior draft
> exists at `docs/reviews/gpt55-xhigh-full-code-review.md`; its substantive claims were
> re-checked against the actual code here. Every quality-gate result below was reproduced
> first-hand in this environment (Section 3). One prior claim (the release tag-glob defect)
> was **refuted** and is documented as such in Section 6.

---

## 1. Executive summary & verdict

This branch hardens the `use-harbor` feature set for production: it removes the
Docker/Compose surface, adds artifact-native build/test targets (charm, rock, snap, Go
binaries), adds CI/integration/release GitHub workflows, hardens auth (bcrypt token hashing,
opt-in dev-auth, optional TLS), adds SSRF protection for Charmhub downloads, introduces
Postgres migration version tracking, and ships a large integration/functional test suite and
operator documentation.

The **product code is in good shape**: every static/security gate is green
(`vet`, `lint`, `govulncheck`, `gosec`, `sqlc-diff`, `build`, `test`, `test-race`), the OCI
per-package authorization boundary is sound, the auth hardening is real, and the SSRF guard
denies all non-Canonical hosts.

However, the branch **fails its own required CI gate** and carries several
release-pipeline correctness defects that can mis-publish or mask broken production
artifacts. These are in the build/release plumbing, not the runtime application:

- **Blocking:** CI's `test` job enforces a 70% coverage threshold; actual coverage is
  **63.1%**, so the job exits non-zero and the `required-status-checks` gate fails.
- **High:** the only place CI runs unit tests is the `make coverage` target, which **masks
  failing package tests**; the snap publish step **masks upload failures and can release a
  stale revision**; and the Postgres migration **advisory-lock pattern is unreliable across a
  connection pool** and can crash-loop multi-replica startup.

**Verdict: CHANGES REQUESTED before production sign-off.** No runtime security flaw was
found, but the branch cannot pass its declared CI as written and has high-severity release
correctness issues. All findings are well-scoped with concrete remediations below.

Counts: **Blocking 1 · High 3 · Medium 6 · Low 7 · 1 prior finding refuted.**

---

## 2. Evidence pack

### 2.1 Workspace & branch verification

```text
$ git branch --show-current
prod-ready-use-harbor/complete
$ git rev-parse HEAD
87cec4289830ba14847174d43684fa0143484a6b
$ git rev-parse origin/use-harbor
f910196fef32cea23bb8a8fc1a9bfb5915db3753
$ git merge-base origin/use-harbor HEAD
f910196fef32cea23bb8a8fc1a9bfb5915db3753
$ git status --short
 D docs/plans/ci-operator-workflows-assessment.md
 D docs/plans/ci-release-design.md
 D docs/plans/integration-test-suite.md
 D docs/plans/no-docker-acceptance-matrix.md
 D docs/plans/no-docker-rock-jubilant-spread-migration.md
 D docs/plans/production-readiness.md
?? docs/reviews/gpt55-xhigh-full-code-review.md
?? docs/reviews/gpt55-xhigh-full-code-review-2026-06-09.md
```

> **Workspace note:** the six `docs/plans/*.md` deletions are **uncommitted working-tree
> changes that pre-existed this review**; those files exist at HEAD. This review targets the
> committed HEAD (`87cec42`). Per task constraints the only file written by this review is this
> report.

### 2.2 Diff shape

```text
$ git diff --stat origin/use-harbor...HEAD   # tail
 115 files changed, 14507 insertions(+), 960 deletions(-)
```

Largest areas: `release.yml` (`+536`), `tests/functional/scenarios.go` (`+969`),
`tests/integration/*`, `docs/*`, and hardening edits across `internal/{auth,oci,api,config,repo,charmhub,sync}`.

---

## 3. Commands run and real outputs

All commands run from `/home/gschiano/charm-registry` on Go `go1.26.4 linux/amd64`. None of
the output below is fabricated.

| Command | Result |
|---|---|
| `make test` | **PASS** — all `internal/...` packages `ok` (`repo/db` excluded by design) |
| `make test-race` | **PASS** — no data races; all internal packages `ok` |
| `make vet` | **PASS** — no output |
| `make lint` (`golangci-lint`) | **PASS** — `0 issues.` |
| `make vuln` (`govulncheck`) | **PASS** — `No vulnerabilities found.` (0 affecting code) |
| `make gosec` | **PASS** — `Issues: 0`, `Nosec: 3`, 71 files |
| `make sqlc-diff` | **PASS** — exit 0, no generated-code drift |
| `make build` | **PASS** — built `.bin/charm-registry`, `.bin/charm-registryctl` |
| `go build ./...` | **PASS** — exit 0 |
| `go vet -tags=integration ./tests/integration/...` | **PASS** — exit 0 (tagged suite compiles) |
| `go vet -tags=functional ./tests/functional/...` | **PASS** — exit 0 (tagged suite compiles) |
| `make coverage` | **63.1%** of statements |
| CI threshold replay (`63.1 < 70` via `bc -l`) | **FAIL → exit 1** (see B-1) |
| `grep -rnE 'panic\(|TODO|FIXME' internal/ cmd/` (non-test) | none |

Selected raw output:

```text
# make lint
0 issues.

# make vuln
No vulnerabilities found. Your code is affected by 0 vulnerabilities.

# make gosec (summary)
Files : 71   Lines : 14691   Nosec : 3   Issues : 0

# make coverage (tail)
total: (statements) 63.1%

# CI threshold replay
coverage=63.1%
ci-threshold-result=FAIL (63.1% < 70%) -> exit 1

# make test-race (tail)
ok  internal/service  3.205s
ok  internal/sync     1.274s     # no data races
```

The three `#nosec` annotations were inspected and are legitimately scoped:
`internal/blob/store.go:135` (G304 — path rooted & traversal-checked by `FileStore.path`),
`internal/api/http_resources.go:123` (G705 — non-HTML attachment),
`internal/service/resources.go:422` (G117 — Charmcraft-format auth blob, not a hardcoded credential).

Integration/packaging tools (`juju`, `lxc`, `charmcraft`, `rockcraft`, `snapcraft`, `spread`,
`skopeo`, `actionlint`) are **not installed** in this environment, so the charm/Jubilant,
snap/spread, and rock workflows were reviewed statically from source; this is recorded as a
limitation, not asserted as a pass.

---

## 4. Findings

Severity: **Blocking** > **High** > **Medium** > **Low**.

### Blocking

#### B-1 — CI `test` job fails its own 70% coverage threshold (actual 63.1%)
- **Files:** `.github/workflows/ci.yml:61-89` (gate at 74-82), `.github/workflows/ci.yml:196-218` (required-status-checks fans in `test`).
- **Evidence (reproduced):** The `test` job runs `make coverage`, then:
  ```bash
  COVERAGE=$(go tool cover -func=coverage.out | grep '^total:' | awk '{print $NF}' | tr -d '%')
  THRESHOLD=70
  if [ "$(echo "${COVERAGE} < ${THRESHOLD}" | bc -l)" -eq 1 ]; then exit 1; fi
  ```
  Local `make coverage` reports `total: (statements) 63.1%`. Replaying the gate expression
  yields `FAIL (63.1% < 70%) -> exit 1`. The `required-status-checks` job (`needs: [...test...]`,
  line 199) checks `needs.test.result == success` and exits 1 otherwise.
- **Impact:** Every push/PR run on this branch produces a red `test` job and a red required
  gate. The branch cannot pass its declared CI as written, blocking any merge/release that
  depends on the gate. (63.1% coverage is not itself dangerous; the defect is the gate config
  vs. actual.)
- **Remediation:** Either raise coverage to ≥70% (target the under-tested `internal/sync`
  error helpers — see §6), or deliberately ratchet `THRESHOLD` to the accepted baseline and
  document the ratchet policy in `docs/testing.md` with a plan to raise it.

### High

#### H-1 — `make coverage` masks failing tests, and it is CI's only test execution
- **Files:** `Makefile` `coverage:` target; `.github/workflows/ci.yml:71-72`.
- **Evidence:** The target is a single compound shell recipe:
  ```make
  @for pkg in $(_COVER_PKGS); do \
      tmp_cov=$$(mktemp); \
      $(GO) test $$pkg -coverprofile=$$tmp_cov -covermode=count >/dev/null; \
      if [ -s "$$tmp_cov" ]; then tail -n +2 "$$tmp_cov" >> coverage.out; fi; \
      rm -f "$$tmp_cov"; \
  done
  ```
  There is no `set -e` and the loop body's last command is `rm -f`, so the for-loop's exit
  status (and thus the recipe's) reflects `rm`, not `go test`. A failing package test is
  silently swallowed. Critically, the CI `test` job runs **only** `make coverage` — there is
  no separate `make test`/`make audit` step in any CI job (lint job → vet/lint/tidy/sqlc;
  security job → vuln/gosec). So unit-test failures are observed by CI **only** through this
  masked target.
- **Impact:** A genuinely failing unit test will pass CI as long as coverage ≥ threshold —
  defeating the test gate for a "production-ready" branch.
- **Remediation:** Add `set -e` (or explicit status capture + non-zero exit) to the loop, and
  run `make test` (fail-fast) as its own CI step before/independent of coverage.

#### H-2 — Snap publish masks upload failures and can release a stale revision
- **File:** `.github/workflows/release.yml:468-495` (upload at 475, fallback at 479).
- **Evidence:**
  ```bash
  OUTPUT=$(snapcraft upload "$SNAP_FILE" 2>&1) || true
  REVISION=$(echo "$OUTPUT" | grep -oP 'Revision \K[0-9]+' || echo "")
  if [ -z "$REVISION" ]; then
    REVISION=$(snapcraft list-revisions charm-registry 2>/dev/null | head -1 | awk '{print $1}')
  fi
  ```
  `|| true` suppresses an upload failure; if revision parsing then fails, the workflow falls
  back to the newest **existing** store revision (`list-revisions | head -1`), which may be
  unrelated to the just-built `.snap`, and proceeds to `snapcraft release` it.
- **Impact:** Authentication/network/upload failures are hidden, a stale snap revision can be
  promoted to a channel, and operators are told the new build shipped when it did not — a
  supply-chain/provenance risk.
- **Remediation:** Drop `|| true`; fail immediately on upload error; parse the revision from a
  reliable source and abort (not fall back) if parsing fails; optionally verify the revision
  hash matches the built artifact before release.

#### H-3 — Postgres migration advisory lock is unreliable across the connection pool
- **File:** `internal/repo/postgres.go:49-109` (lock 62-72); invoked unconditionally at startup by `internal/app/app.go:58`.
- **Evidence:** The lock is taken with `p.pool.QueryRow(... pg_try_advisory_lock ...)` and
  released with `p.pool.QueryRow(... pg_advisory_unlock ...)`. These are **session-scoped**
  locks, but `pgxpool` serves each call from an arbitrary pooled connection:
  1. Migration statements (lines 95-106) run on different connections than the lock holder, so
     the lock does not actually serialize the migration body on one session.
  2. The unlock very likely runs on a connection that does not hold the lock → returns `false`
     → the lock **leaks** and remains held by the original backend session until that pooled
     connection is recycled (pgx default `MaxConnLifetime` ~1h / `MaxConnIdleTime` ~30m).
  3. `pg_try_advisory_lock` is non-blocking: a second instance starting concurrently gets
     `locked=false` → `Migrate` returns the error at line 67 → `app.New` fails → the replica
     **crash-loops** instead of waiting.
- **Impact:** This ships as a Juju charm (multi-unit expected). Simultaneous startup can
  crash-loop all-but-one unit until a pooled connection recycles. Migrations are idempotent
  (`IF NOT EXISTS`), so data is safe, but rollouts stall. Single-replica deploys are unaffected.
- **Remediation:** Acquire a **dedicated** connection (`pool.Acquire(ctx)`) and run the
  blocking `pg_advisory_lock` + all migration statements + `pg_advisory_unlock` on that same
  `*pgxpool.Conn`. Prefer blocking `pg_advisory_lock` (or a bounded retry on the try-lock) so
  concurrent starters wait rather than error out.

### Medium

#### M-1 — Manual (`workflow_dispatch`) release does not check out the requested `tag`
- **File:** `.github/workflows/release.yml` — `workflow_dispatch.inputs.tag` (lines 14-17); no checkout step sets `ref:` (verified: `grep -n 'ref:' release.yml` returns nothing; checkouts at lines 95, 123, 166, 201, 223, 248, 280, 336 all use defaults).
- **Evidence/Impact:** On a manual run the operator supplies a `tag` input that labels the
  GitHub release / rock / charm / snap / SBOM, but every job checks out the UI-selected ref
  (default `github.ref`), not that tag. Artifacts can therefore be built from a different
  commit than the tag they are published under — a provenance mismatch. The normal **tag-push**
  path is correct (checkout defaults to the pushed tag), so the defect is scoped to manual
  dispatch.
- **Remediation:** For manual releases, validate the tag exists and add
  `with: ref: ${{ needs.release-metadata.outputs.tag }}` to every build/publish checkout (or
  fail unless `github.ref_name == inputs.tag`), and record the checked-out SHA in the release
  summary/attestation.

#### M-2 — Default 30s `WriteTimeout` on the embedded OCI server can truncate large blob transfers
- **File:** `cmd/charm-registry/main.go:72-85` (`newOCIServer` sets `WriteTimeout: cfg.ServerWriteTimeout`); default `30s` in `internal/config/config.go:250`.
- **Evidence/Impact:** The embedded distribution registry serves OCI image/rock blobs that are
  routinely hundreds of MB. A fixed 30s server write deadline aborts any client pull/push that
  exceeds it (e.g. 200 MB at < ~55 Mbps). Juju deploy/refresh pulling OCI resources on slower
  links would see truncated transfers with a non-obvious cause. (Server-side mirroring in
  `internal/oci/client.go` uses its own `c.transport` and is **not** affected.)
- **Remediation:** Set the OCI server's `WriteTimeout: 0` (rely on `IdleTimeout` +
  `ReadHeaderTimeout`) or add a separate large OCI-specific timeout knob; at minimum document
  raising `CHARM_REGISTRY_SERVER_WRITE_TIMEOUT`.

#### M-3 — IP rate limiter keys on a spoofable header and never schedules cleanup
- **File:** `internal/api/http.go:442-499` (`Allow`/`rateLimit`), `470-485` (`Cleanup`), `60` (`RealIP`).
- **Evidence:** `rateLimit` takes the IP from `r.Header.Get("X-Forwarded-For")` (first hop) —
  attacker-controlled. A client can rotate the header to get a fresh bucket per request,
  bypassing the per-IP limit. Separately, `ipRateLimiter.Cleanup()` exists but **no caller
  schedules it** (verified by grep); `Allow` only prunes the accessed key, so map entries for
  one-shot IPs persist for the process lifetime (slow growth). `chimiddleware.RealIP` is
  already installed, so re-parsing the raw header is both redundant and the spoofable path.
- **Impact:** Per-IP throttling is bypassable; the limiter map can grow unbounded. (The
  token-issue limiter is keyed on the authenticated account ID and self-cleans — unaffected.)
- **Remediation:** Key on `r.RemoteAddr` (with `RealIP` configured to trust only known
  proxies), drop the in-handler `X-Forwarded-For` parse, and run a periodic `Cleanup()`
  goroutine; for multi-instance, use a shared store (the code comment at line 421 acknowledges this).

#### M-4 — Migration `0008` requires elevated DB privilege and builds an index non-concurrently
- **File:** `internal/repo/migrations/0008_search_trgm.sql`.
- **Evidence:** `CREATE EXTENSION IF NOT EXISTS pg_trgm;` typically needs database-owner /
  `rds_superuser`; on locked-down managed Postgres the migration (and startup) fails.
  `CREATE INDEX ... USING gin (...)` (no `CONCURRENTLY`, and it cannot be, since migrations run
  in a transaction) takes an exclusive lock during the build, blocking writes on an upgrade of
  a populated `packages` table.
- **Remediation:** Document the `pg_trgm` privilege/pre-provisioning requirement in
  `docs/deployment.md`/`docs/operations.md`, emit a clear operator-facing error on extension
  failure, and consider an out-of-transaction `CONCURRENTLY` maintenance path for the index.

#### M-5 — `make snap-integration-test` runs a stray local functional-test after spread
- **File:** `Makefile` `snap-integration-test:` target (verified via `make -n`).
- **Evidence:** After the `spread -v ./tests/spread/...` block, the target unconditionally runs
  `FTEST_API_URL=http://localhost:8080 .bin/functional-test`, which executes **outside** the
  spread backend against a local service that may not be running and a binary that may be stale
  or missing.
- **Impact:** A successful spread run can be followed by an unrelated local-service failure →
  confusing false negatives in CI/local.
- **Remediation:** Remove the trailing command or split it into a separate, documented target
  (with a `functional-test-build` dependency and an explicit local-service prerequisite).

#### M-6 — `spread.yaml` restore hook typo `--purse` prevents snap purge
- **File:** `spread.yaml:27` — `snap remove --purse charm-registry || true`.
- **Evidence/Impact:** The valid option is `--purge`; `--purse` errors, and `|| true` hides it,
  so snap data/services leak between spread runs → order-dependent or hidden failures.
- **Remediation:** Fix to `snap remove --purge charm-registry || true`; consider shellcheck/
  smoke-validation of spread snippets.

### Low

- **L-1 — Stale Docker/Compose references in a doc shipped on this branch.**
  `docs/quality-gate-report-consolidated.md` (added in commit `e450784`) still references
  `docker compose -f compose.integration.yaml`, `repo tests excluded — need Docker`, and lists
  `Dockerfile`/`compose.integration.yaml` (lines 41/47/81/83) — contradicting the no-Docker
  story and superseded by `docs/reviews/quality-gate-report-2026-06-08.md` and
  `docs/reviews/no-docker-final-verification.md`. *Fix:* update or remove the stale report.
- **L-2 — Token prefix collisions can falsely reject a valid token.**
  `internal/auth/auth.go:187-228` + `internal/repo/queries/accounts.sql:66` (`:one`). Prefixes
  are `"cr_"` + 5 base64 chars (~30 bits); `FindStoreTokenByPrefix` returns one arbitrary row,
  so a colliding bcrypt token can fail auth (it then falls back to a SHA-256 lookup that cannot
  find a bcrypt row). **No false accept** is possible — `bcrypt.CompareHashAndPassword` still
  gates acceptance. *Fix:* query `:many` and bcrypt-compare each candidate, or widen the prefix.
- **L-3 — Unauthenticated `libraries/bulk` drains the body without a cap.**
  `internal/api/http_libraries.go` uses `io.Copy(io.Discard, r.Body)` with no
  `http.MaxBytesReader` (route is intentionally anonymous, `internal/api/http.go:77`). Bounded
  by the 30s timeouts; memory-safe but a minor unauthenticated CPU/bandwidth sink. *Fix:* wrap
  in `http.MaxBytesReader`.
- **L-4 — Router `Timeout` is hardcoded 30s, ignoring configured server timeouts.**
  `internal/api/http.go:62`. Combined with M-2, large API uploads/downloads can be cut
  regardless of operator config. *Fix:* drive from config or exempt large-transfer routes.
- **L-5 — `OCISecretKey` rotation unsupported/undocumented.**
  `internal/oci/client.go:50-52,554-578`: robot secrets are sealed under a key derived with a
  fixed salt and tagged `v1`; changing `CHARM_REGISTRY_OCI_SECRET_KEY` makes all stored
  `EncryptedSecret` undecryptable. *Fix:* document immutability and/or add a versioned
  re-encryption path.
- **L-6 — Two byte-limit config values not validated `> 0`.**
  `internal/config/config.go:380-404` validates `MaxArchiveFileBytes`, the Charmhub limits, and
  `OCIMaxManifestBytes`, but not `MaxJSONBodyBytes`/`MaxUploadBytes`. *Fix:* add `> 0` checks.
- **L-7 — `/metrics` is served unauthenticated** (`internal/api/http.go:72`,
  `internal/api/metrics.go`). Acceptable if scraped only over a trusted network, but the
  exposure should be explicit/configurable or bound to an internal listener. *Fix:* document
  network placement or gate it.

---

## 5. Areas reviewed in depth (and why they are sound)

- **Authentication (`internal/auth/auth.go`).** Bearer/Macaroon schemes; bcrypt with
  transparent SHA-256→bcrypt upgrade on successful auth; prefix-indexed lookup with bcrypt
  verification gating acceptance; `revoked_at`/`valid_until` enforced; dev-auth strictly gated
  behind `EnableInsecureDevAuth` (defaults false, with a startup warning at
  `cmd/charm-registry/main.go:28-30`). No false-accept path found.
- **Service-layer authorization.** Commit `b68aab4` adds `requireAuth(identity)` ahead of
  resource lookups across packages/releases/resources/revisions, so unauthenticated requests
  return `401` rather than leaking existence via `404`. Routes wrap handlers in
  `requireIdentity` (`internal/api/http.go:79-121`); `requireAuth`
  (`internal/service/helpers.go:18-26`) correctly allows `System` identities for internal sync.
- **OCI per-package authorization (`internal/oci/client.go:406-499`).** The middleware extracts
  the project from `/v2/<project>/.../{blobs,manifests,tags,referrers}/...`, resolves the
  package, and validates BasicAuth against that package's push (mutating) or pull/push (read)
  robot credentials with `subtle.ConstantTimeCompare`. `_catalog` and short paths fall to a
  `401` challenge (no enumeration). Cross-package access is prevented by package-unique
  credentials. Secrets are AES-GCM-sealed; the former `panic` in `deriveKey` is now an error.
- **SSRF guard (`internal/charmhub/client.go:411-512`).** Downloads must be `https`; only the
  configured base host and `*.charmhub.io`/`*.juju.is`/`*.canonical.com` are allowed (all other
  hosts rejected), with redirect re-validation (max 5 hops) and size-bounded reads.
- **CI/release security posture.** `ci.yml`/`integration.yml` run least-privilege
  (`permissions: contents: read`); `release.yml` scopes `contents/packages/attestations/id-token:
  write` for OIDC publishing and defaults `dry_run: true`. All third-party actions are SHA-pinned.
- **Test isolation.** Integration tests are gated `//go:build integration`, functional tests
  `//go:build functional`; both tagged suites compile (Section 3). `make test`/`make test-race`
  pass.

---

## 6. Prior-finding correction & non-blocking recommendations

### Refuted prior claim — release tag-glob trigger is **not** a defect
The prior draft flagged the `release.yml` tag triggers
`v[0-9]+.[0-9]+.[0-9]+*` / `v[0-9]+.[0-9]+.[0-9]+*-rc[0-9]+` as broken globs that would not
match normal semver tags. **This is incorrect.** GitHub Actions ref-filter patterns are not
plain globs: `+` means "one or more of the preceding character", `[]` is a character class,
`*` is "zero or more non-`/`", and `.` is literal. So `v[0-9]+.[0-9]+.[0-9]+*` correctly
matches `v1.2.3` and `v1.2.3-rc1`. The second pattern is redundant but harmless. No change
required (optionally simplify to a single `v[0-9]+.[0-9]+.[0-9]+*` plus the existing
regex-validation step).

### Recommendations
1. Add `actionlint` to CI (and a local target) to lint workflow syntax/triggers.
2. Add regression tests for IP-limiter forwarded-header trust + cleanup (M-3) and bcrypt
   prefix-collision handling (L-2).
3. Raise coverage on `internal/sync` error helpers (`newErrorWithCause`, `translateRepoError`
   show 0.0%) to help close B-1 honestly rather than only lowering the threshold.
4. Make integration-test gating explicit in `docs/release.md`: which jobs gate PR vs. tag vs.
   manual acceptance (today integration runs are `workflow_dispatch`/weekly only — not a release gate).
5. Add a migration preflight section to `docs/operations.md` (pg_trgm privilege, advisory-lock
   behavior, recovery).
6. Resolve the uncommitted `docs/plans/*` deletions in the working tree.

---

## 7. Docker / Compose / testcontainers cleanup — completeness confirmation

**Status: COMPLETE at the source/build/runtime level; one stale documentation reference remains.**

Evidence:
- `Dockerfile`, `compose.yaml`, `.dockerignore` are **deleted** in the branch delta
  (`Dockerfile | 21 -`, `compose.yaml | 180 -`, `.dockerignore | 7 -`) and absent from the tree.
- `git ls-files | grep -iE 'docker|compose|testcontainer'` returns only **documentation** files
  (`docs/plans/no-docker-*.md`, `docs/reviews/no-docker-final-verification.md`). **No
  testcontainers** dependency or usage anywhere.
- Remaining `docker` tokens in tracked, non-doc files are **legitimate OCI terminology /
  transitive deps**, explicitly within the task's allowed set:
  - `internal/oci/client.go:355` — `"/docker/registry/v2/repositories"`, the on-disk storage
    layout of the `distribution/distribution` registry.
  - `internal/service/resources.go:422` — a Charmcraft-format ("Docker-style") auth blob
    (`#nosec G117`), not a Docker dependency.
  - `go.mod`/`go.sum` — `github.com/docker/{cli,docker-credential-helpers,go-events,go-metrics}`
    as **indirect** dependencies of `distribution/distribution/v3` and `go-containerregistry`
    (the OCI registry libraries). Expected, not a Docker runtime path.
  - `docker://` skopeo transport references in release/deployment docs.
- **Only wart:** stale `docker compose`/`Dockerfile` references in
  `docs/quality-gate-report-consolidated.md` (finding **L-1**) — documentation drift, not a
  build/runtime regression.

---

## 8. Summary table

| ID | Severity | Area | File(s) |
|----|----------|------|---------|
| B-1 | **Blocking** | CI coverage gate fails (63.1% < 70%) | `.github/workflows/ci.yml:74-82,196-218` |
| H-1 | High | `make coverage` masks test failures; only CI test path | `Makefile` `coverage:`, `ci.yml:71-72` |
| H-2 | High | Snap publish masks upload failure / stale revision | `.github/workflows/release.yml:468-495` |
| H-3 | High | PG migration advisory lock unreliable across pool | `internal/repo/postgres.go:49-109` |
| M-1 | Medium | Manual release ignores requested tag (no `ref:`) | `.github/workflows/release.yml` |
| M-2 | Medium | OCI server WriteTimeout truncates large blobs | `cmd/charm-registry/main.go:72-85` |
| M-3 | Medium | IP rate-limit spoofable + unbounded growth | `internal/api/http.go:442-499,470-485` |
| M-4 | Medium | `pg_trgm` privilege + non-concurrent index | `internal/repo/migrations/0008_search_trgm.sql` |
| M-5 | Medium | snap-integration-test stray functional-test | `Makefile` `snap-integration-test:` |
| M-6 | Medium | spread restore typo `--purse` | `spread.yaml:27` |
| L-1 | Low | Stale Docker refs in shipped doc | `docs/quality-gate-report-consolidated.md` |
| L-2 | Low | Token prefix collision (false reject only) | `internal/auth/auth.go`, `accounts.sql:66` |
| L-3 | Low | Unauth body drain without cap | `internal/api/http_libraries.go` |
| L-4 | Low | Hardcoded router timeout | `internal/api/http.go:62` |
| L-5 | Low | OCI secret rotation unsupported | `internal/oci/client.go:50-52,554-578` |
| L-6 | Low | Config validation gap | `internal/config/config.go:380-404` |
| L-7 | Low | `/metrics` unauthenticated | `internal/api/http.go:72` |

**Bottom line:** the runtime application (auth, OCI scoping, SSRF, secrets, storage) is sound
and all code-quality/security gates pass. The branch is **not yet production-ready** because it
fails its own required CI coverage gate (B-1) and has high-severity release/test-pipeline
defects (H-1, H-2) plus a multi-replica migration hazard (H-3). Address the Blocking/High
findings (or explicitly risk-accept H-3 for single-replica) and the branch is in good shape.

## 9. Review limitations
- `juju`, `lxc`, `charmcraft`, `rockcraft`, `snapcraft`, `spread`, `skopeo`, and `actionlint`
  are not installed here, so charm/Jubilant, snap/spread, rock, and workflow-lint paths were
  reviewed statically from source, not executed end-to-end.
- This was a review/report-only task; no product code was modified. The only file written is
  this report.
