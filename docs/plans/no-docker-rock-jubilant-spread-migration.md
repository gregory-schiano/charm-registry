# No-Docker Rock/Jubilant/Spread Migration Plan

> **For Hermes:** Use subagent-driven-development skill to implement this plan task-by-task. Kanban tasks for this plan should be assigned to the `qwen37max` Hermes profile, configured with OpenRouter model `qwen/qwen3.7-max`.

**Goal:** Remove Docker and Docker Compose from charm-registry development, testing, CI, release, and documentation; build the OCI artifact from `rockcraft.yaml`; run functional integration coverage as charm integration tests with Jubilant and snap integration tests with spread; and consolidate all implementation branches into `prod-ready-use-harbor/complete`.

**Architecture:** Treat the charm, snap, and rock as first-class production artifacts. Charm integration tests deploy the charm with Juju and exercise the service through real charm relations and config. Snap spread tests install the snap in a VM/backend, configure its service, and exercise the same user-visible API flows. The rock is built with Rockcraft and published by CI instead of any Dockerfile-based image path.

**Tech Stack:** Go 1.26 module; Charmcraft `go-framework` charm; Rockcraft `go-framework` rock; Snapcraft core24 snap; Jubilant for Juju/charm integration tests; spread for snap tests; GitHub Actions; Canonical `canonical/operator-workflows` reusable workflows where applicable.

---

## Hard Requirements

- Base every work branch on the existing `prod-ready-use-harbor/complete` branch, not `main`.
- Do not merge into `main`.
- Use isolated worktrees for implementation cards.
- Assign cards to the `qwen37max` profile (`qwen/qwen3.7-max`).
- Remove repo-owned Docker and Docker Compose usage entirely:
  - Delete `Dockerfile`, `compose.yaml`, `compose.integration.yaml`, and Docker/Compose Make targets.
  - Remove Docker-based docs and historical plan recommendations or rewrite them as superseded notes.
  - CI must not call `docker build`, `docker compose`, `docker/login-action`, `docker/metadata-action`, or `docker/build-push-action`.
- Build OCI images through `rockcraft.yaml`; if publication needs an OCI push, use Rockcraft/Skopeo/ORAS/registry tooling that does not rely on a Docker daemon or Dockerfile.
- Charm integration tests must use Jubilant and execute the relevant functional integration scenarios.
- Snap integration tests must use spread and execute the relevant functional integration scenarios.
- CI must build and test all production artifacts: Go binaries, charm, rock, and snap.
- CI release path must publish/release all artifacts, with dry-run and protected real-publish modes documented.
- Final fan-in branch is `prod-ready-use-harbor/complete`.

## Existing State to Change

- `Makefile` currently exposes `make up`, `make down`, `integration-up`, `integration-down`, `integration-run`, and `integration-test` built on Docker Compose.
- `.github/workflows/ci.yml` currently builds binaries and then runs `docker build --tag charm-registry:ci --file Dockerfile .`.
- `rockcraft.yaml`, `charm/charmcraft.yaml`, and `snap/snapcraft.yaml` exist but are not yet first-class CI artifacts.
- Existing integration tests under `tests/integration/` are Go tests against a Compose stack.
- Existing docs and historical plans mention Docker Compose and Docker-image releases.

## Dependency Graph

```text
T01 baseline inventory
├── T02 remove Docker/Compose surface
│   ├── T03 rock build and smoke test
│   └── T04 shared functional test harness
│       ├── T05 Jubilant charm integration tests
│       └── T06 spread snap integration tests
├── T07 CI build/test workflow
│   ├── T08 CI publish/release workflow
│   └── T09 artifact docs and human docs cleanup
└── T10 final consolidation and verification
```

The branch for T10 must merge or cherry-pick every accepted task branch into `prod-ready-use-harbor/complete` and then run final verification.

---

## Task T01: Baseline Inventory and Acceptance Matrix

**Objective:** Establish the exact Docker references, artifact commands, and functional scenarios before code changes.

**Branch:** `prod-ready-use-harbor/no-docker-baseline`

**Files:**
- Create: `docs/plans/no-docker-acceptance-matrix.md`
- Inspect: `Makefile`, `.github/workflows/*.yml`, `Dockerfile`, `compose.yaml`, `compose.integration.yaml`, `rockcraft.yaml`, `charm/charmcraft.yaml`, `snap/snapcraft.yaml`, `tests/integration/`, `README.md`, `docs/`

**Steps:**
1. Run `git grep -n -Ei 'docker|compose|Dockerfile|build-push-action|metadata-action|login-action'` and capture every repo-owned reference.
2. Run baseline commands that do not require Docker:
   - `make tidy-check`
   - `make vet`
   - `make test`
   - `make build`
3. Probe artifact tooling availability:
   - `charmcraft --version || true`
   - `rockcraft --version || true`
   - `snapcraft --version || true`
   - `spread --version || true`
   - `juju version || true`
   - `python3 -c 'import jubilant; print(jubilant.__version__)' || true`
4. Build an acceptance matrix mapping each existing integration scenario to either a charm/Jubilant test, snap/spread test, or both.
5. Commit the matrix.

**Verification:**
- Matrix names every current Docker/Compose reference and states delete/replace/keep-with-rationale.
- Matrix identifies which functional scenarios are covered by charm integration, snap integration, and unit tests.
- No implementation changes beyond documentation/inventory.

---

## Task T02: Remove Docker and Compose Surface

**Objective:** Delete Docker/Compose artifacts and replace developer commands with artifact-native commands.

**Branch:** `prod-ready-use-harbor/remove-docker-compose`

**Files:**
- Delete: `Dockerfile`
- Delete: `compose.yaml`
- Delete: `compose.integration.yaml`
- Modify: `Makefile`
- Modify: `.gitignore` if Docker-specific temporary artifacts are tracked there
- Modify: `.env.example` only if it contains Compose-only assumptions

**Steps:**
1. Remove `SHARED_DOCKER_NETWORK`, `up`, `down`, `COMPOSE_ITEST`, `integration-up`, `integration-down`, and Docker-specific `integration-test` targets from `Makefile`.
2. Add or keep artifact-native targets:
   - `make build`
   - `make charm-pack` → runs `charmcraft pack` from `charm/`.
   - `make rock-pack` → runs `rockcraft pack` from repo root.
   - `make snap-pack` → runs `snapcraft pack` from repo root.
   - `make artifact-build` → runs charm, rock, snap package builds.
   - `make charm-integration-test` → invokes the Jubilant suite.
   - `make snap-integration-test` → invokes spread.
3. Ensure no Make target shells out to Docker or Docker Compose.
4. Delete the Docker/Compose YAML/files.
5. Commit the deletion/replacement.

**Verification:**
- `git grep -n -Ei 'docker|compose|Dockerfile' -- Makefile Dockerfile compose.yaml compose.integration.yaml` returns no live Docker/Compose implementation path. It is acceptable only for historical release notes that explicitly say superseded.
- `make help` no longer advertises Docker/Compose.
- `make build` still passes.

---

## Task T03: Rockcraft OCI Artifact Build and Smoke Test

**Objective:** Make `rockcraft.yaml` the only OCI image definition and prove the rock can run without Docker.

**Branch:** `prod-ready-use-harbor/rockcraft-oci`

**Files:**
- Modify: `rockcraft.yaml`
- Create: `scripts/rock-smoke-test.sh` if a repeatable local smoke test is useful
- Modify: `Makefile`
- Modify: `.github/workflows/ci.yml` later via T07 if this task proves the command shape

**Steps:**
1. Review `rockcraft.yaml` and ensure it packages both `charm-registry` and any required runtime assets/certs/scripts.
2. Replace placeholder comments with explicit `parts`/`organize` entries only where needed.
3. Run `rockcraft pack` or document the required LXD/destructive-mode command if the local environment blocks it.
4. Smoke test the resulting `.rock` using non-Docker tooling. Preferred paths:
   - Use `skopeo inspect oci-archive:<rock>` or Rockcraft-provided inspection.
   - If runtime execution is required, use Canonical Kubernetes/Juju charm path instead of Docker.
5. Commit rockcraft changes.

**Verification:**
- A `.rock` artifact is produced locally or the blocker is concrete and reproducible.
- The rock contains the expected Go binary and starts through its configured Pebble service when run in the intended Canonical runtime.
- No Dockerfile is used or reintroduced.

---

## Task T04: Shared Functional Test Harness Without Compose

**Objective:** Extract reusable client/test helpers from the current Go integration suite so charm and snap tests can share behavior without relying on Compose orchestration.

**Branch:** `prod-ready-use-harbor/shared-functional-tests`

**Files:**
- Modify: `tests/integration/` or move helpers into `tests/functional/`
- Create: `tests/functional/README.md`
- Modify: `go.mod` only if a small test helper dependency is justified

**Steps:**
1. Identify test logic that can run against any externally provided base URL, admin credential, and OCI endpoint.
2. Refactor helper code so it accepts environment variables instead of assuming `localhost` Compose ports.
3. Keep scenario coverage focused on behavior:
   - health/readiness
   - admin bootstrap/dev-auth behavior where appropriate
   - package registration
   - revision upload/download
   - release/channel behavior
   - resource/OCI credentials where feasible
   - persistence/restart through the orchestrator-specific suite, not a Compose restart
4. Add commands for running the harness against an already running service.
5. Commit helper-only changes before charm/snap-specific wrappers.

**Verification:**
- Unit tests pass.
- Functional helpers compile and can be called with externally supplied endpoints.
- No helper invokes Docker or Compose.

---

## Task T05: Jubilant Charm Integration Tests

**Objective:** Replace Compose integration with Juju/Jubilant tests executed as part of charm testing.

**Branch:** `prod-ready-use-harbor/jubilant-charm-tests`

**Files:**
- Create: `tests/integration/charm/` or `tests/charm/`
- Create: `tests/integration/charm/test_charm_registry.py`
- Create/modify: `pyproject.toml` or `tox.ini` if a Python test environment is needed
- Modify: `Makefile`
- Modify: `charm/charmcraft.yaml` only if testability exposes missing relations/config

**Steps:**
1. Add Python test dependencies for `pytest` and `jubilant` in the project’s chosen Python test config.
2. Write Jubilant tests that:
   - Pack or consume the local charm.
   - Deploy PostgreSQL and S3-compatible relations suitable for the charm.
   - Deploy `charm-registry` charm.
   - Configure public API/storage/registry URLs.
   - Wait for active/idle.
   - Exercise the shared functional scenarios through the deployed application endpoint.
3. Add restart/persistence coverage through Juju action/restart or unit reschedule rather than Compose restart.
4. Add `make charm-integration-test`.
5. Commit tests and config.

**Verification:**
- `make charm-pack` succeeds.
- `make charm-integration-test` runs Jubilant tests or blocks with an explicit missing Juju/LXD prerequisite.
- Tests do not import or shell out to Docker/Compose.

---

## Task T06: Spread Snap Integration Tests

**Objective:** Add spread tests that install the snap, configure the service, and exercise production-relevant functional scenarios.

**Branch:** `prod-ready-use-harbor/spread-snap-tests`

**Files:**
- Create: `spread.yaml`
- Create: `tests/spread/charm-registry/task.yaml`
- Create supporting scripts under `tests/spread/` if needed
- Modify: `snap/snapcraft.yaml` if tests reveal missing plugs, commands, config hooks, or service behavior
- Modify: `Makefile`

**Steps:**
1. Define spread systems appropriate for snap testing, starting with Ubuntu 24.04/core24-compatible targets.
2. Build or consume the locally packed snap in spread prepare steps.
3. Install the snap with dangerous/local install mode.
4. Configure service settings and credentials using `snap set` or documented snap commands.
5. Start/restart the snap daemon and wait for health.
6. Run the shared functional scenarios through the snap-exposed endpoint.
7. Add `make snap-integration-test`.
8. Commit spread tests.

**Verification:**
- `make snap-pack` succeeds.
- `make snap-integration-test` runs spread tests or blocks with a concrete missing spread/snapd/LXD prerequisite.
- Tests do not invoke Docker/Compose.

---

## Task T07: CI Build and Test Workflow

**Objective:** Update CI so every PR builds and tests Go code, the charm, the rock, and the snap, and runs charm/snap integration suites in the right lanes.

**Branch:** `prod-ready-use-harbor/ci-artifact-tests`

**Files:**
- Modify: `.github/workflows/ci.yml`
- Add additional workflow files if clearer, e.g. `.github/workflows/integration.yml`
- Reference: local clone of `canonical/operator-workflows`

**Steps:**
1. Keep Go lint/security/unit jobs, including `tidy-check`, `vet`, `lint`, `govulncheck`, `gosec`, coverage threshold, and binary build.
2. Add charm packaging and charm integration jobs. Prefer canonical/operator-workflows reusable workflows for charm packing/testing where they match this repository; otherwise document why a local job is needed and keep the job shape aligned with operator-workflows conventions.
3. Add rock packaging job using Rockcraft, with artifact upload for `.rock`.
4. Add snap packaging and spread test job, with artifact upload for `.snap`.
5. Remove Docker image build/inspect steps.
6. Update required status check fan-in to include artifact and integration jobs.
7. Commit workflow changes.

**Verification:**
- `git grep -n -Ei 'docker|compose|Dockerfile|build-push-action|metadata-action|login-action' .github/workflows` returns no Docker CI path.
- Workflow syntax validates with `actionlint` if available.
- CI clearly uploads charm, rock, snap, and binary artifacts.

---

## Task T08: Publish and Release Workflows

**Objective:** Add protected release workflows that publish charm, rock, and snap artifacts.

**Branch:** `prod-ready-use-harbor/release-publishing`

**Files:**
- Create/modify: `.github/workflows/release.yml`
- Modify: `docs/release.md` or create it
- Reference: `canonical/operator-workflows` publish/promote workflows

**Steps:**
1. Define release triggers: tags and manual `workflow_dispatch` with dry-run option.
2. Publish charm through Canonical Charmhub workflows/actions, using `canonical/operator-workflows` where applicable.
3. Publish rock to the intended OCI registry using Rockcraft-compatible or OCI-native tooling without Docker. Document required secrets.
4. Publish snap via Snap Store action/tooling and document channels/tracks.
5. Upload SBOM/provenance/checksum artifacts where practical.
6. Ensure dry-run mode builds all artifacts without publishing.
7. Commit workflows and release docs.

**Verification:**
- Release workflow has a dry-run path that does not require store credentials.
- Real publish path is protected by tag/manual trigger and documented secrets.
- No Docker daemon, Dockerfile, or Compose usage appears in release jobs.

---

## Task T09: Documentation Refresh and Human Review

**Objective:** Rewrite docs so they describe the production artifact story clearly, accurately, and without Docker-era sloppiness.

**Branch:** `prod-ready-use-harbor/docs-no-docker`

**Files:**
- Modify: `README.md`
- Modify/create: `docs/deployment.md`
- Modify/create: `docs/testing.md`
- Modify/create: `docs/release.md`
- Modify historical plan docs only when they are misleading as current guidance

**Steps:**
1. Replace Docker Compose local deployment instructions with snap/charm/rock-native paths.
2. Document developer commands for Go unit tests, artifact builds, Jubilant tests, and spread tests.
3. Document artifact release channels and required credentials.
4. Remove unnecessary detail and stale implementation speculation.
5. Keep a human tone: explain why charm/snap/rock paths exist and when to use each.
6. Run a docs grep for stale Docker guidance.
7. Commit docs changes.

**Verification:**
- README has a clear quick start that does not mention Docker/Compose as the active path.
- `docs/testing.md` covers unit, charm integration, and snap spread tests.
- `docs/release.md` covers charm, rock, and snap release flows.
- Any remaining `docker` string is either protocol/domain terminology such as OCI internals or an explicit historical note, not a command users should run.

---

## Task T10: Final Consolidation and Verification

**Objective:** Merge all accepted work branches into `prod-ready-use-harbor/complete`, verify the repository is Docker-free, and produce the final status report.

**Branch:** `prod-ready-use-harbor/complete`

**Files:**
- Modify/create: `docs/reviews/no-docker-final-verification.md`

**Steps:**
1. Check out `prod-ready-use-harbor/complete`.
2. Merge/cherry-pick accepted branches in dependency order:
   - `prod-ready-use-harbor/no-docker-baseline`
   - `prod-ready-use-harbor/remove-docker-compose`
   - `prod-ready-use-harbor/rockcraft-oci`
   - `prod-ready-use-harbor/shared-functional-tests`
   - `prod-ready-use-harbor/jubilant-charm-tests`
   - `prod-ready-use-harbor/spread-snap-tests`
   - `prod-ready-use-harbor/ci-artifact-tests`
   - `prod-ready-use-harbor/release-publishing`
   - `prod-ready-use-harbor/docs-no-docker`
3. Audit every worktree for untracked and modified files before deleting anything.
4. Run final verification:
   - `make tidy-check`
   - `make vet`
   - `make lint`
   - `make test`
   - `make coverage`
   - `make build`
   - `make charm-pack`
   - `make rock-pack`
   - `make snap-pack`
   - `make charm-integration-test` where environment permits
   - `make snap-integration-test` where environment permits
   - `git grep -n -Ei 'docker|compose|Dockerfile|build-push-action|metadata-action|login-action'` and classify all remaining matches
5. Write final verification report with pass/fail/blocker evidence.
6. Commit final report.
7. Send Telegram progress/final notification.

**Verification:**
- `prod-ready-use-harbor/complete` contains all accepted task work.
- No implementation, CI, test, or active docs path relies on Docker or Docker Compose.
- Charm, rock, and snap artifacts are all built in CI.
- Charm integration tests run through Jubilant.
- Snap integration tests run through spread.

---

## Kanban Creation Notes

Create cards in tenant `charm-registry-no-docker-use-harbor` on board `default`. Use:

- `--assignee qwen37max`
- `--workspace worktree`
- `--branch <branch listed above>`
- idempotency keys prefixed `no-docker-use-harbor-`
- parent links matching the dependency graph
- `--skill software-development/subagent-driven-development` when implementation/review discipline is useful
- `--skill using-git-worktrees` for worktree tasks

After card creation, verify with:

```bash
hermes kanban list --tenant charm-registry-no-docker-use-harbor
hermes kanban stats
hermes profile show qwen37max
```

Report all task IDs and branch names before workers begin substantial implementation.
