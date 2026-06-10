# Final Merged Code Review — `prod-ready-use-harbor/complete`

- **Reviewer profile:** gpt55xhigh (GPT 5.5, xhigh reasoning)
- **Date:** 2026-06-10
- **HEAD reviewed:** `87cec4289830ba14847174d43684fa0143484a6b`
- **Base:** `origin/use-harbor` (delta: 115 files, +14,507 / −960)
- **Sources merged:** `gpt55-xhigh-full-code-review.md` (06-08) + `gpt55-xhigh-full-code-review-2026-06-09.md` (06-09)

## Verdict

**CHANGES REQUESTED.** Runtime application (auth, OCI scoping, SSRF, secrets, storage, sync)
is sound; all code-quality/security gates pass (`vet`, `lint`, `govulncheck`, `gosec`,
`sqlc-diff`, `build`, `test`, `test-race`). Branch is not production-ready: it fails its own
CI coverage gate and has high-severity release/test-pipeline and HA-migration defects.

Counts: **Blocking 1 · High 3 · Medium 7 · Low 9 · Refuted 1.**

## Quality gates (reproduced)

| Gate | Result | Gate | Result |
|---|---|---|---|
| `make test` | PASS | `make build` / `go build ./...` | PASS |
| `make test-race` | PASS (no races) | `make sqlc-diff` | PASS (no drift) |
| `make vet` | PASS | tagged test compile (integration/functional) | PASS |
| `make lint` | PASS (0 issues) | `make coverage` | 63.1% |
| `make vuln` | PASS (0 in code) | CI threshold (70%) replay | FAIL → exit 1 |
| `make gosec` | PASS (0 issues) | | |

## Blocking

| ID | Finding | Location | Remediation |
|---|---|---|---|
| B-1 | CI `test` job fails its own 70% coverage threshold (actual 63.1%); `required-status-checks` fans it in | `.github/workflows/ci.yml:74-82,196-218` | Raise coverage ≥70% or ratchet `THRESHOLD` to baseline + document |

## High

| ID | Finding | Location | Remediation |
|---|---|---|---|
| H-1 | `make coverage` masks failing package tests (no `set -e`; loop exit = `rm`) and is CI's only test execution | `Makefile` `coverage:`, `ci.yml:71-72` | Add `set -e`/status capture; run `make test` fail-fast as its own CI step |
| H-2 | Snap publish masks upload failure (`\|\| true`) and falls back to a stale `list-revisions` revision | `.github/workflows/release.yml:468-495` | Fail on upload error; abort (not fall back) if revision parse fails |
| H-3 | Postgres migration advisory lock acquired/released on arbitrary pooled connections; non-blocking try-lock crash-loops concurrent multi-unit startup | `internal/repo/postgres.go:49-109` (via `internal/app/app.go:58`) | Run blocking `pg_advisory_lock` + migrations + unlock on one `pool.Acquire()` connection |

## Medium

| ID | Finding | Location | Remediation |
|---|---|---|---|
| M-1 | Manual (`workflow_dispatch`) release does not check out the requested `tag` input | `.github/workflows/release.yml` (no `ref:` in any checkout) | Add `ref: <tag>` to every build/publish checkout; record built SHA |
| M-2 | Default 30s server `WriteTimeout` on embedded OCI server can truncate large image/rock blob transfers | `cmd/charm-registry/main.go:72-85`; `internal/config/config.go:250` | Set OCI server `WriteTimeout: 0` or a large OCI-specific knob |
| M-3 | IP rate limiter keys on spoofable `X-Forwarded-For` (bypass) and never schedules `Cleanup()` (unbounded growth) | `internal/api/http.go:442-499,470-485,60` | Key on `RealIP`-normalized `RemoteAddr`; run periodic cleanup |
| M-4 | Migration `0008` `CREATE EXTENSION pg_trgm` needs elevated privilege; non-`CONCURRENTLY` index locks on upgrade | `internal/repo/migrations/0008_search_trgm.sql` | Document privilege/pre-provisioning; clear error on failure |
| M-5 | `make snap-integration-test` runs a stray local `functional-test` against `localhost:8080` after spread | `Makefile` `snap-integration-test:` | Remove or split into a separate documented target |
| M-6 | `spread.yaml` restore hook typo `--purse` (masked by `\|\| true`) leaves snap state between runs | `spread.yaml:27` | Fix to `snap remove --purge` |
| M-7 | Integration suites run only `workflow_dispatch`/weekly cron — not gated on PR or release | `.github/workflows/integration.yml:3-8`; `release.yml` | Gate a smoke subset on PR; require integration for release tags |

## Low

| ID | Finding | Location | Remediation |
|---|---|---|---|
| L-1 | Stale `docker compose`/`Dockerfile` references in a doc shipped on this branch | `docs/quality-gate-report-consolidated.md:41,47,81-83` | Update or remove (superseded report) |
| L-2 | 30-bit token prefix collision; `:one` lookup can false-reject a colliding bcrypt token (no false-accept) | `internal/auth/auth.go:187-228`; `internal/repo/queries/accounts.sql:66` | Query `:many` + bcrypt-compare each, or widen prefix |
| L-3 | Unauthenticated `libraries/bulk` drains body without `MaxBytesReader` | `internal/api/http_libraries.go`; route `internal/api/http.go:77` | Wrap body in `http.MaxBytesReader` |
| L-4 | Router `Timeout` hardcoded 30s, ignores configured server timeouts | `internal/api/http.go:62` | Drive from config or exempt large-transfer routes |
| L-5 | `OCISecretKey` rotation unsupported/undocumented (fixed salt, `v1`) | `internal/oci/client.go:50-52,554-578` | Document immutability and/or add versioned re-encryption |
| L-6 | `MaxJSONBodyBytes` / `MaxUploadBytes` not validated `> 0` | `internal/config/config.go:380-404` | Add `> 0` validation |
| L-7 | `/metrics` served unauthenticated | `internal/api/http.go:72`; `internal/api/metrics.go` | Gate, bind to internal listener, or document network placement |
| L-8 | OCI provisioning errors expose backend detail to authorized clients | `internal/service/oci.go:57-68`; `internal/api/http.go:323-326` | Keep cause in logs; return generic client message |
| L-9 | Trailing-whitespace `git diff --check` failures in raw review notes | `docs/reviews/raw/code-review-glm-5.1.md:3-5,40,59` | Strip trailing whitespace |

## Refuted

| Finding | Status |
|---|---|
| Release tag-glob trigger `v[0-9]+.[0-9]+.[0-9]+*` "won't match semver tags" | **Not a defect** — GitHub ref filters treat `+`=one-or-more, `[]`=class, `*`=zero-or-more, `.`=literal; pattern matches `v1.2.3`/`v1.2.3-rc1` |

## Docker / Compose / testcontainers cleanup

**COMPLETE** at source/build/runtime level: `Dockerfile`, `compose.yaml`, `.dockerignore`
deleted; no testcontainers. Remaining tokens are legitimate — `/docker/registry/v2` storage
path (`internal/oci/client.go:355`), Docker-style auth blob (`internal/service/resources.go:422`),
indirect `github.com/docker/*` OCI library deps in `go.mod`/`go.sum`, `docker://` skopeo refs in
docs. Only residue: stale doc reference (L-1).

## Recommendations

1. Add `actionlint` to CI for workflow syntax/trigger linting.
2. Add regression tests: IP-limiter forwarded-header/cleanup (M-3), bcrypt prefix collision (L-2).
3. Raise `internal/sync` error-helper coverage (`newErrorWithCause`, `translateRepoError` at 0.0%).
4. Document migration preflight (pg_trgm privilege, advisory-lock behavior, recovery) in `docs/operations.md`.
5. Resolve uncommitted `docs/plans/*` working-tree deletions.

## Limitations

`juju`, `lxc`, `charmcraft`, `rockcraft`, `snapcraft`, `spread`, `skopeo`, `actionlint` not
installed; charm/snap/spread/rock/workflow-lint paths reviewed statically. Review-only task; no
product code modified.
