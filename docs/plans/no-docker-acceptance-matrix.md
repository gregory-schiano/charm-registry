# No-Docker Acceptance Matrix

> Task T01 baseline inventory. Establishes the exact Docker references, artifact commands,
> and functional scenarios before code changes.

**Branch:** `prod-ready-use-harbor/no-docker-baseline`
**Date:** 2026-06-08

> Resolution update: commit `7c61b5d` removed the `internal/repo` unit-test dependency on `testcontainers-go` by porting the former container-backed repository behavior tests to SQLite. `make test` now passes without Docker. The baseline failures below are retained as historical pre-migration evidence.

---

## 1. Baseline Command Results

| Command | Result | Notes |
|---|---|---|
| `make tidy-check` | PASS | All modules verified |
| `make vet` | PASS | `go vet ./cmd/... ./internal/...` clean |
| `make build` | PASS | `charm-registry` and `charm-registryctl` built to `.bin/` |
| `make test` | PARTIAL | 10/11 packages pass; `internal/repo` fails |

### Unit Test Detail

The `internal/repo` package uses `testcontainers-go` (via `testcontainers-go/modules/postgres`)
which panics at init trying to connect to `/var/run/docker.sock`. This is a **Docker dependency
in the unit test path** that should be addressed independently of the integration migration.

| Package | Result |
|---|---|
| `internal/api` | PASS |
| `internal/app` | PASS |
| `internal/auth` | PASS |
| `internal/blob` | PASS |
| `internal/charm` | PASS |
| `internal/charmhub` | PASS |
| `internal/config` | PASS |
| `internal/oci` | PASS |
| `internal/service` | PASS |
| `internal/sync` | PASS |
| `internal/repo` | **FAIL** — testcontainers-go requires Docker socket |

---

## 2. Artifact Tooling Availability

| Tool | Version | Status |
|---|---|---|
| `charmcraft` | — | NOT INSTALLED |
| `rockcraft` | — | NOT INSTALLED |
| `snapcraft` | — | NOT INSTALLED |
| `spread` | — | NOT INSTALLED |
| `juju` | — | NOT INSTALLED |
| `jubilant` (Python) | — | NOT INSTALLED |

All Canonical artifact tooling is absent from this host. CI jobs must install or use
container-provided runners. Local developer commands should document installation prerequisites.

---

## 3. Docker/Compose Reference Inventory

### 3.1 Files to DELETE

| File | Rationale |
|---|---|
| `Dockerfile` | OCI image build moves to `rockcraft.yaml` |
| `compose.yaml` | Local dev stack replaced by snap/charm-native paths |
| `compose.integration.yaml` | Integration tests move to Jubilant/spread |

### 3.2 Workflow Files to MODIFY

| File | Line(s) | Reference | Action |
|---|---|---|---|
| `.github/workflows/ci.yml` | 109–114 | `docker build`, `docker inspect` | DELETE build/inspect steps |
| `.github/workflows/ci.yml` | 120–121 | Docker Compose comment | REWRITE comment |
| `.github/workflows/integration.yml` | 18, 31–32, 37–38, 58, 71, 73, 75, 77 | `SHARED_DOCKER_NETWORK`, `docker network create`, `docker compose up/down/logs` | REPLACE with Jubilant/spread CI jobs |
| `.github/workflows/release.yml` | 112–136 | `docker/setup-qemu-action`, `docker/setup-buildx-action`, `docker/metadata-action`, `docker/login-action`, `docker/build-push-action` | REPLACE with Rockcraft publish |

### 3.3 Makefile Targets to MODIFY

| Target | Line(s) | Current | Action |
|---|---|---|---|
| `SHARED_DOCKR_NETWORK` | 3 | `charm-registry-shared` | DELETE |
| `help` (up/down descriptions) | 26–30 | Compose stack references | REWRITE for artifact-native commands |
| `up` | 127–128 | `docker network create`, `docker compose up --build -d` | DELETE |
| `down` | 131 | `docker compose down -v` | DELETE |
| `COMPOSE_ITEST` | 135 | `docker compose -f compose.integration.yaml` | DELETE |
| `integration-up` | 147 | `$(COMPOSE_ITEST) up --build -d` | DELETE |
| `integration-down` | 151 | `$(COMPOSE_ITEST) down -v` | DELETE |
| `integration-test` | — | Docker-dependent test runner | REPLACE with artifact-native runner |

### 3.4 Documentation Files to MODIFY

| File | Line(s) | Content | Action |
|---|---|---|---|
| `.env.example` | 4, 9, 34 | Docker host / compose port references | REWRITE with snap/charm-native config |
| `README.md` | 68–82, 125, 199, 228, 278–279, 308 | Compose stack, docker login, deployment guide | REWRITE for snap/charm/rock paths |
| `AGENT.md` | 78, 80, 123, 185–186, 197, 207, 353 | Compose config verification instructions | REWRITE for snap/charm config |
| `SECURITY.md` | 54, 65 | Hardened compose profile mentions | REWRITE for snap/charm security |
| `docs/backup-restore.md` | 27–30, 49–52, 85, 103, 129 | Docker Compose backup/restore procedures | REWRITE for native deployment paths |
| `docs/configuration.md` | 50 | OCI host port via compose | REWRITE |
| `docs/deployment.md` | 3–47, 171 | Docker Compose as primary deployment target | DELETE Docker Compose section, keep snap/rock |
| `docs/operations.md` | 165 | Docker image installation mention | REWRITE |
| `docs/plans/ci-release-design.md` | Multiple | Historical Docker Compose CI design | KEEP as historical — add superseded note |
| `docs/plans/integration-test-suite.md` | Multiple | Historical Compose-based test design | KEEP as historical — add superseded note |
| `docs/plans/production-readiness.md` | 9, 24, 31, 48–49, 61, 69, 177, 279 | Historical Docker references | KEEP as historical documentation |

### 3.5 Review/Analysis Files (KEEP AS HISTORICAL)

| File | Content | Action |
|---|---|---|
| `docs/quality-gate-report-consolidated.md` | Integration test results with Docker dependencies | KEEP — historical report |
| `docs/reviews/aligned-code-review-backlog.md` | Review findings referencing compose.yaml | KEEP — historical review artifact |
| `docs/reviews/aligned-feature-roadmap.md` | Feature analysis mentioning compose | KEEP — historical review artifact |
| `docs/reviews/quality-gate-report-2026-06-08.md` | Docker-based integration findings | KEEP — historical report |
| `docs/reviews/raw/*.md` | Individual review files | KEEP — historical review artifacts |

### 3.6 Source Code References

| File | Line(s) | Reference | Action |
|---|---|---|---|
| `internal/oci/client.go` | 355 | `"/docker/registry/v2/repositories"` path | **KEEP** — OCI distribution v2 API path standard, not Docker daemon |
| `internal/service/resources.go` | 422 | `"Docker-style auth blob"` comment | **KEEP** — Charmcraft expects Docker registry auth format |
| `go.mod` / `go.sum` | Multiple | `docker/*` indirect dependencies via `distribution/v3` | **KEEP** — Go library dependencies, not Docker daemon usage |

### 3.7 Scripts to MODIFY

| File | Line(s) | Content | Action |
|---|---|---|---|
| `scripts/wait-for-healthy.sh` | 4, 24–26 | Docker Compose health check | DELETE or REPLACE with artifact-native health check |
| `deploy/oci/generate-certs.sh` | 69 | Comment referencing compose image UID | REWRITE comment reference |

### 3.8 Integration Tests to MIGRATE

| File | Line(s) | Content | Action |
|---|---|---|---|
| `tests/integration/integration_test.go` | 17 | Compose lifecycle comment | MIGRATE to jubilant/spread |
| `tests/integration/persist_test.go` | 15 | Container restart via compose | MIGRATE to Juju restart or snap restart |

---

## 4. Integration Test Scenario Map

### 4.1 Current Test Scenarios (80+ tests across 12 files)

| Scenario ID | Category | Test Name | Charm/Jubilant | Snap/Spread | Unit Tests |
|---|---|---|:---:|:---:|:---:|
| BOOT-01 | Bootstrap | Service starts healthy | ✓ | ✓ | — |
| BOOT-02 | Bootstrap | Root document metadata | ✓ | ✓ | — |
| BOOT-03 | Bootstrap | OpenAPI spec served | ✓ | ✓ | — |
| BOOT-04 | Bootstrap | Docs page rendered | ✓ | ✓ | — |
| BOOT-05 | Bootstrap | Security headers present | ✓ | ✓ | — |
| BOOT-06 | Bootstrap | OCI registry catalog | ✓ | — | — |
| PKG-01 | Package CRUD | Register charm | ✓ | ✓ | ✓ |
| PKG-02 | Package CRUD | Register bundle | ✓ | ✓ | ✓ |
| PKG-03 | Package CRUD | Register private package | ✓ | ✓ | ✓ |
| PKG-04 | Package CRUD | Register defaults to charm type | ✓ | ✓ | ✓ |
| PKG-05 | Package CRUD | Duplicate returns conflict | ✓ | ✓ | ✓ |
| PKG-06 | Package CRUD | Invalid name returns bad request | ✓ | ✓ | ✓ |
| PKG-07 | Package CRUD | No auth → unauthorized | ✓ | ✓ | ✓ |
| PKG-08 | Package CRUD | List packages | ✓ | ✓ | ✓ |
| PKG-09 | Package CRUD | List no auth → unauthorized | ✓ | ✓ | ✓ |
| PKG-10 | Package CRUD | List metadata fields | ✓ | ✓ | ✓ |
| PKG-11 | Package CRUD | Get package | ✓ | ✓ | ✓ |
| PKG-12 | Package CRUD | Get metadata fields | ✓ | ✓ | ✓ |
| PKG-13 | Package CRUD | Get nonexistent → 404 | ✓ | ✓ | ✓ |
| PKG-14 | Package CRUD | Get no auth → 401 | ✓ | ✓ | ✓ |
| PKG-15 | Package CRUD | Private pkg forbidden to other user | ✓ | ✓ | ✓ |
| PKG-16 | Package CRUD | Patch package metadata | ✓ | ✓ | ✓ |
| PKG-17 | Package CRUD | Patch persists across GET | ✓ | ✓ | ✓ |
| PKG-18 | Package CRUD | Patch nonexistent → 404 | ✓ | ✓ | ✓ |
| PKG-19 | Package CRUD | Patch no auth → 401 | ✓ | ✓ | ✓ |
| PKG-20 | Package CRUD | Delete package | ✓ | ✓ | ✓ |
| PKG-21 | Package CRUD | Deleted pkg → 404 | ✓ | ✓ | ✓ |
| PKG-22 | Package CRUD | Delete nonexistent → 404 | ✓ | ✓ | ✓ |
| PKG-23 | Package CRUD | Delete no auth → 401 | ✓ | ✓ | ✓ |
| PKG-24 | Package CRUD | Full CRUD lifecycle | ✓ | ✓ | ✓ |
| PKG-25 | Package CRUD | In list after register | ✓ | ✓ | ✓ |
| PKG-26 | Package CRUD | Removed from list after delete | ✓ | ✓ | ✓ |
| PKG-27 | Package CRUD | Latest default track | ✓ | ✓ | ✓ |
| PKG-28 | Package CRUD | Empty type defaults to charm | ✓ | ✓ | ✓ |
| PKG-29 | Package CRUD | Toggle private flag | ✓ | ✓ | ✓ |
| REV-01 | Revision | Full upload-to-release pipeline | ✓ | ✓ | — |
| REV-02 | Revision | Upload with resources | ✓ | — | — |
| REV-03 | Revision | List revisions | ✓ | ✓ | — |
| REV-04 | Revision | Release to multiple channels | ✓ | ✓ | — |
| REV-05 | Revision | Create and use custom track | ✓ | ✓ | — |
| REV-06 | Revision | Charm download | ✓ | ✓ | — |
| REV-07 | Revision | Unscanned upload returns upload ID | ✓ | ✓ | — |
| REV-08 | Revision | Push revision returns status URL | ✓ | ✓ | — |
| REV-09 | Revision | Release returns released array | ✓ | ✓ | — |
| REV-10 | Revision | Revision metadata fields | ✓ | ✓ | — |
| TOKEN-01 | Token | Issue with dev auth | ✓ | ✓ | — |
| TOKEN-02 | Token | List after issue | ✓ | ✓ | — |
| TOKEN-03 | Token | Exchange token | ✓ | ✓ | — |
| TOKEN-03b | Token | Offline exchange | ✓ | ✓ | — |
| TOKEN-04 | Token | Revoke token | ✓ | ✓ | — |
| TOKEN-04b | Token | Revoke includes macaroons | ✓ | ✓ | — |
| TOKEN-05 | Token | Whoami with token | ✓ | ✓ | — |
| TOKEN-05b | Token | Token whoami | ✓ | ✓ | — |
| TOKEN-06 | Token | Expired token rejected | ✓ | ✓ | — |
| TOKEN-07 | Token | Rate limiting | ✓ | ✓ | — |
| TOKEN-08 | Token | Scoped permissions | ✓ | ✓ | — |
| TOKEN-09 | Token | Package scoping | ✓ | ✓ | — |
| TOKEN-10 | Token | Channel scoping | ✓ | ✓ | — |
| TOKEN-11 | Token | Unauthenticated challenge | ✓ | ✓ | — |
| TOKEN-12 | Token | Dashboard exchange | ✓ | ✓ | — |
| TOKEN-13 | Token | Deprecated whoami | ✓ | ✓ | — |
| TOKEN-14 | Token | Unauthenticated whoami → 401 | ✓ | ✓ | — |
| TOKEN-15 | Token | Unauthenticated token whoami → 401 | ✓ | ✓ | — |
| TOKEN-16 | Token | List includes inactive | ✓ | ✓ | — |
| TOKEN-17 | Token | Preserve description | ✓ | ✓ | — |
| TOKEN-18 | Token | Issue no auth dev mode | ✓ | ✓ | — |
| TOKEN-19 | Token | Exchange no creds → 401 | ✓ | ✓ | — |
| TOKEN-20 | Token | Macaroon header auth | ✓ | ✓ | — |
| ACL-01 | Access Control | Owner access own private pkg | ✓ | ✓ | ✓ |
| ACL-02 | Access Control | Different user → 403 | ✓ | ✓ | ✓ |
| ACL-03 | Access Control | Private hidden from other list | ✓ | ✓ | ✓ |
| ACL-04 | Access Control | Other user can access public pkg | ✓ | ✓ | ✓ |
| ACL-05 | Access Control | Unauthenticated → 401 | ✓ | ✓ | ✓ |
| ACL-06 | Access Control | Owner full CRUD on private | ✓ | ✓ | ✓ |
| ACL-07 | Access Control | Different user cannot release | ✓ | ✓ | ✓ |
| JUJU-01 | Juju Compat | Charm download endpoint | ✓ | — | — |
| JUJU-02 | Juju Compat | Nonexistent revision → 404 | ✓ | — | — |
| JUJU-03 | Juju Compat | Download without auth → 401 | ✓ | — | — |
| JUJU-04 | Juju Compat | Download public as other user | ✓ | — | — |
| JUJU-05 | Juju Compat | Unscanned upload endpoint | ✓ | — | — |
| OCI-01 | OCI Registry | Catalog endpoint | ✓ | — | — |
| OCI-02 | OCI Registry | V2 base endpoint | ✓ | — | — |
| OCI-03 | OCI Registry | Upload credentials | ✓ | — | — |
| OCI-04 | OCI Registry | Image blob endpoint | ✓ | — | — |
| OCI-05 | OCI Registry | TLS certificate valid | ✓ | — | — |
| OCI-06 | OCI Registry | Blob HEAD digest | ✓ | — | — |
| PERSIST-01a | Persistence | Package data persists | ✓ | ✓ | — |
| PERSIST-01b | Persistence | Token data persists | ✓ | ✓ | — |
| PERSIST-02 | Persistence | Revision data persists | ✓ | ✓ | — |
| PERSIST-03 | Persistence | Release data persists | ✓ | ✓ | — |
| PERSIST-04 | Persistence | V2 info reflects persisted data | ✓ | ✓ | — |
| LIM-01 | Rate Limiting | Token issue rate limited | ✓ | ✓ | ✓ |
| LIM-02 | Rate Limiting | Per-account limiting | ✓ | ✓ | ✓ |
| LIM-03 | Rate Limiting | Non-token endpoints not limited | ✓ | ✓ | ✓ |
| LIM-04 | Rate Limiting | Large request body rejected | ✓ | ✓ | ✓ |
| RES-01 | Resources | List declared resources | ✓ | ✓ | — |
| RES-02 | Resources | Empty list for no resources | ✓ | ✓ | — |
| RES-03 | Resources | Revisions after push | ✓ | ✓ | — |
| RES-04 | Resources | Push returns status URL | ✓ | ✓ | — |
| RES-05 | Resources | Download returns content | ✓ | ✓ | — |
| RES-06 | Resources | Nonexistent → 404 or empty | ✓ | ✓ | — |
| V2-01 | V2 API | Find by query | ✓ | ✓ | — |
| V2-02 | V2 API | Empty query → valid response | ✓ | ✓ | — |
| V2-03 | V2 API | Info with channels | ✓ | ✓ | — |
| V2-04 | V2 API | Specific channel info | ✓ | ✓ | — |
| V2-05 | V2 API | Nonexistent package → 404 | ✓ | ✓ | — |
| V2-06 | V2 API | Refresh resolves known package | ✓ | ✓ | — |
| V2-07 | V2 API | No auth → 401 on all V2 | ✓ | ✓ | — |

### 4.2 Coverage Strategy

| Category | Charm/Jubilant | Snap/Spread | Reasoning |
|---|:---:|:---:|---|
| **Bootstrap** (BOOT) | ✓ | ✓ | Both must prove service starts and serves |
| **Package CRUD** (PKG) | ✓ | ✓ | Core API, exercise in both deployment modes |
| **Revision** (REV) | ✓ | ✓ | Upload/release/download are core workflows |
| **Token** (TOKEN) | ✓ | ✓ | Auth infrastructure, both modes |
| **Access Control** (ACL) | ✓ | ✓ | Security-critical, both modes |
| **Juju Compat** (JUJU) | ✓ | — | Juju-specific download endpoints; snap doesn't need these |
| **OCI Registry** (OCI) | ✓ | — | Embedded OCI is charm-deployment specific; snap can embed but OCI testing via charm path is sufficient |
| **Persistence** (PERSIST) | ✓ | ✓ | Restart/reschedule via Juju unit action or `snap restart` |
| **Rate Limiting** (LIM) | ✓ | ✓ | Behavior-critical, both modes |
| **Resources** (RES) | ✓ | ✓ | Charm resource operations are core workflow |
| **V2 API** (V2) | ✓ | ✓ | Charmhub v2 compatibility, both modes |

### 4.3 Existing Unit Test Coverage

10 packages pass unit tests without Docker:
- `internal/api`, `internal/app`, `internal/auth`, `internal/blob`, `internal/charm`,
  `internal/charmhub`, `internal/config`, `internal/oci`, `internal/service`, `internal/sync`

1 package requires Docker for unit tests:
- `internal/repo` — uses `testcontainers-go/modules/postgres` for Postgres integration at unit level

**Recommendation:** Replace `testcontainers-go` in `internal/repo` with either:
- Mock-based Postgres repo tests (unit level, no Docker)
- Spread-based or separate CI job for Postgres integration tests

---

## 5. Artifact Definitions (Current State)

| Artifact | File | Status |
|---|---|---|
| **Rock** | `rockcraft.yaml` | Exists, uses `go-framework` extension, bare base, `go/1.26/stable` build-snap. Not wired into CI. |
| **Charm** | `charm/charmcraft.yaml` | Exists, uses `go-framework` extension, requires `postgresql`, `s3`, `oci-s3`, `openid`, `oci-ingress` relations. Not wired into CI. |
| **Snap** | `snap/snapcraft.yaml` | Exists, core24 base, `go/1.26/stable` build-snap, strict confinement, daemon with network/network-bind plugs. Not wired into CI. |

None of the three artifacts are currently built in CI (T07 addresses this).

---

## 6. Summary

- **Total Docker/Compose references:** ~150 matches across 30+ files
- **Files to delete:** 3 (`Dockerfile`, `compose.yaml`, `compose.integration.yaml`)
- **Files to modify:** 12 (Makefile, 3 workflows, 8 docs, 1 script)
- **Files to keep as historical:** 10+ (review reports, historical plans)
- **Source code to keep:** 3 files (OCI path constants, Go indirect deps) — not Docker daemon usage
- **Integration test scenarios:** 80+ tests across 12 files, all tagged `//go:build integration`
- **Unit test blocker:** `internal/repo` requires Docker socket via testcontainers-go
- **Artifact tooling:** All Canonical tools (charmcraft, rockcraft, snapcraft, spread, juju, jubilant) are unavailable on this host — CI runners must provide them
- **Baseline:** tidy-check ✓, vet ✓, build ✓ — clean foundation for migration
