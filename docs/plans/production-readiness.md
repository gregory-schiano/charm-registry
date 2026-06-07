# Charm Registry Production Readiness Implementation Plan

> **For Hermes:** Use subagent-driven-development skill to implement this plan task-by-task. Use worktrees for every implementation/review card. Do not commit on `main`, and do not merge into `main` until all gates pass and the user explicitly approves.

**Goal:** Turn the `use-harbor` branch of the vibe-coded private Charmhub-compatible registry into a production-ready service with aligned multi-model review, integration coverage, CI/release automation, documentation, and a prioritized feature roadmap.

**Architecture:** Treat this as a gated fan-out/fan-in program. Run independent read-only reviews in parallel across Claude Opus 4.7, GPT 5.5, and GLM 5.1, then synthesize a single aligned change plan before applying changes. Implementation happens in isolated worktrees, with tests and docs updated before CI/release workflows are finalized.

**Tech Stack:** Go 1.25 module with Go 1.26.1 toolchain, chi HTTP API, Postgres/sqlc repository, S3-compatible blob storage, Docker Compose, GitHub Actions, Canonical `operator-workflows` reusable workflows where applicable.

---

## Grounding from inspection

- Repository root: `/home/gschiano/charm-registry`
- Required base branch: `origin/use-harbor` (`f910196 Add snap + Support TLS` at correction time).
- All production-readiness work must start from `use-harbor`, not `main`. Earlier cards under tenant `charm-registry-prod-ready` were created from `main` and are superseded.
- Corrected planning worktree created at: `/home/gschiano/.config/superpowers/worktrees/charm-registry/production-readiness-use-harbor`
- Corrected planning branch: `production-readiness-use-harbor`
- Original planning branch `production-readiness-plan` was mistakenly based on `main`; use this corrected branch instead.
- Existing tests: 7 Go test files, mainly unit/handler tests; no dedicated live integration suite found.
- Existing CI: `.github/workflows/ci.yml` runs lint, security, coverage, and build but does not publish artifacts/releases.
- Existing docs: README + SECURITY only; README has MVP tone and a local absolute `.env.example` link that should be fixed.
- Local verification blocker: `make test` failed because `go` is not installed in this execution environment (`/bin/sh: 1: go: not found`). Docker is available, so CI parity can be verified through Docker or by installing Go 1.26.1 before continuing.
- Canonical `operator-workflows` readme confirms reusable workflows such as `test.yaml`, `integration_test.yaml`, `publish_charm.yaml`, `promote_charm.yaml`, and docs workflows. Note: these workflows target charm/operator repositories and tox-based workflows, so this Go service likely needs a small compatibility layer or selective reuse instead of blindly replacing the existing Go CI.

---

## Gate policy

1. Pre-flight gate: establish a working Go toolchain or Docker-based equivalent; run `make test` and `make build` before any implementation.
2. Model alignment gate: no code changes from reviews until Opus 4.7, GPT 5.5, and GLM 5.1 findings are compared and reconciled into one accepted backlog.
3. Feature alignment gate: no roadmap commitments until the three model feature analyses are compared and reconciled.
4. Test gate: integration tests must fail meaningfully before implementation where new behavior is required, then pass locally.
5. CI gate: GitHub Actions must execute unit, integration, lint, security, build, artifact upload, and release/publish paths.
6. Docs gate: documentation must explain real features with human, useful prose and avoid filler or speculative claims.
7. Final gate: full local verification plus CI status before any merge proposal.

---

## Task graph

### Task 1: Pre-flight toolchain and baseline verification

**Objective:** Make local verification repeatable before touching product code.

**Files:**
- Inspect: `go.mod`, `Makefile`, `.github/workflows/ci.yml`, `Dockerfile`, `compose.yaml`
- Possibly create: `scripts/ci-go.sh` or `scripts/dev-go.sh` only if needed to run Go through Docker consistently

**Steps:**
1. In an isolated worktree, check whether `go` is available.
2. If unavailable, either install Go 1.26.1 in the workspace environment or run test/build commands inside `golang:1.26.1-bookworm` with bind-mounted module caches.
3. Run `make test`.
4. Run `make build`.
5. Run `make coverage` if toolchain setup is stable.
6. Record exact commands and outputs in the Kanban card.

**Verification:**
- `make test` passes or the card blocks with a concrete toolchain/install command for approval.
- `make build` produces `.bin/charm-registry` or equivalent Docker-built artifact.

### Task 2: Code review with Claude Opus 4.7

**Objective:** Produce a read-only code review focused on correctness, security, data model, API compatibility, deployment risk, and test gaps.

**Files:**
- Review all production code under `cmd/` and `internal/`
- Review `Makefile`, `Dockerfile`, `compose.yaml`, `.github/workflows/ci.yml`, `.golangci.yml`, `.env.example`, `README.md`, `SECURITY.md`

**Steps:**
1. Run a read-only review using Claude Opus 4.7 from a clean worktree.
2. Ask for issues grouped as Critical, High, Medium, Low.
3. Require concrete file/line references and proposed fixes.
4. Require a separate section for false-positive risks or uncertain claims.
5. Do not apply changes in this task.

**Verification:**
- Review artifact exists as a Kanban summary/comment or `docs/reviews/code-review-opus-4.7.md`.

### Task 3: Code review with GPT 5.5

Same scope and output format as Task 2, using GPT 5.5.

### Task 4: Code review with GLM 5.1

Same scope and output format as Task 2, using GLM 5.1.

### Task 5: Synthesize aligned code-review backlog

**Objective:** Compare the three code reviews and create a single accepted backlog before implementation.

**Depends on:** Tasks 2, 3, 4

**Steps:**
1. Normalize all findings by component and severity.
2. Mark each finding as: agreed by 3/3, agreed by 2/3, single-model but compelling, or rejected/needs reproduction.
3. For each accepted finding, write the smallest safe implementation step and its verification command.
4. Identify findings that require user decisions before implementation.

**Verification:**
- One aligned backlog exists with no duplicate or contradictory items.
- Every accepted code change has a test or verification path.

### Task 6: Feature-set analysis with Claude Opus 4.7

**Objective:** Assess the current feature set against a production private Charmhub-compatible registry.

**Focus areas:**
- Juju/charmcraft compatibility
- Auth/OIDC/token lifecycle
- ACL/group management
- charm libraries, bundles, resources, revisions, releases, tracks
- operational endpoints and observability
- migration/backup/restore
- admin UX/API gaps
- OCI registry integration risks

**Verification:**
- Feature-gap review artifact exists; no code changes applied.

### Task 7: Feature-set analysis with GPT 5.5

Same scope and output format as Task 6, using GPT 5.5.

### Task 8: Feature-set analysis with GLM 5.1

Same scope and output format as Task 6, using GLM 5.1.

### Task 9: Synthesize aligned feature roadmap

**Objective:** Compare three feature analyses and produce a prioritized roadmap.

**Depends on:** Tasks 6, 7, 8

**Output:**
- Must-have before production
- Should-have soon after production
- Nice-to-have / explicitly out of scope
- Compatibility validation checklist with stock `juju` and `charmcraft`

**Verification:**
- Roadmap is aligned and explicitly flags model disagreements.

### Task 10: Integration test design

**Objective:** Design an integration suite that covers relevant functional aspects instead of only unit/handler behavior.

**Depends on:** Tasks 5 and 9

**Test scenarios to cover:**
1. Boot full stack: Postgres, MinIO, OCI registry, service.
2. Health/root/docs/openapi endpoints.
3. Insecure dev auth happy path for local-only tests.
4. Token issue/list/exchange/revoke lifecycle.
5. Package registration and metadata update.
6. Charm upload, revision retrieval, release to channel/track, and refresh/info/find flows.
7. Resource upload/download lifecycle.
8. ACL/private access negative tests.
9. Body/header/upload limit tests against a real HTTP server.
10. Restart persistence: data survives service restart with Postgres/S3.
11. Basic charmcraft/juju compatibility smoke if viable in CI, otherwise a documented manual gate.

**Verification:**
- A test plan maps every scenario to a command and expected result.

### Task 11: Implement integration tests

**Objective:** Add the integration suite from Task 10.

**Depends on:** Task 10

**Files likely to create/modify:**
- Create: `tests/integration/` or `internal/integration/`
- Create: `scripts/integration-test.sh`
- Modify: `Makefile` with `integration-test` target
- Modify: `compose.yaml` or create `compose.integration.yaml` if needed

**TDD cycle:**
1. Add first failing integration test for token lifecycle against live service.
2. Run it and confirm failure for the expected reason if harness is not yet wired.
3. Wire harness minimally.
4. Repeat for registration/upload/release/refresh/resource/ACL/restart scenarios.
5. Run the full integration suite.

**Verification:**
- `make integration-test` passes locally or blocks with exact missing dependency.

### Task 12: CI and release workflow design using canonical/operator-workflows where useful

**Objective:** Design CI that tests, builds artifacts, and releases them without cargo-culting charm-specific workflows.

**Depends on:** Task 11

**Plan:**
1. Keep Go-native lint/security/unit/build jobs where they are a better fit.
2. Evaluate `canonical/operator-workflows/.github/workflows/integration_test.yaml@main` for integration tests only if a tox compatibility layer is justified.
3. Evaluate `publish_charm.yaml`/`promote_charm.yaml` only if this repository will publish charm artifacts; otherwise build and release the registry binary/container with GitHub-native jobs.
4. Add artifact upload for `.bin/charm-registry`, coverage, and container image digest.
5. Add tag/release workflow with explicit permissions and provenance/SBOM if feasible.

**Verification:**
- CI design doc identifies which operator-workflows are used, which are intentionally not used, and why.

### Task 13: Implement CI/release workflows

**Objective:** Add/adjust workflows so every PR and release runs the right gates.

**Depends on:** Task 12

**Files likely to modify/create:**
- Modify: `.github/workflows/ci.yml`
- Create: `.github/workflows/integration.yml`
- Create: `.github/workflows/release.yml`
- Possibly create: `tox.ini` only if operator-workflows reuse requires it and the cost is justified

**Verification:**
- Local workflow lint if available.
- `make test`, `make build`, `make integration-test` pass locally.
- Workflow syntax validates via `gh workflow`/`act`/GitHub push where available.

### Task 14: Documentation audit and rewrite

**Objective:** Make docs complete, accurate, useful, and human-sounding.

**Depends on:** Task 9

**Files likely to modify/create:**
- Modify: `README.md`
- Modify: `SECURITY.md`
- Modify: `.env.example`
- Create: `docs/architecture.md`
- Create: `docs/configuration.md`
- Create: `docs/deployment.md`
- Create: `docs/api-compatibility.md`
- Create: `docs/testing.md`
- Create: `docs/operations.md`

**Rules:**
- Document only real, verified features.
- Say what is not supported yet.
- Avoid marketing filler.
- Include exact commands for local dev, integration tests, deployment, backup/restore expectations, and release process.
- Fix sloppy details such as absolute local links.

**Verification:**
- Fresh reader can run the stack and understand production caveats without asking the author.

### Task 15: Apply aligned production-hardening changes

**Objective:** Implement accepted code/config changes from the aligned review backlog.

**Depends on:** Tasks 5, 11, 13, 14

**Rules:**
- Implement small changes with TDD where behavior changes.
- Commit each logical change separately on the worktree branch.
- Do not implement single-model speculative suggestions unless reproduced or approved.

**Verification:**
- Unit, integration, lint, security, build all pass.

### Task 16: Final production-readiness verification

**Objective:** Prove the branch is ready for human review.

**Depends on:** Tasks 13, 14, 15

**Commands:**
- `make tidy-check`
- `make vet`
- `make lint`
- `make test`
- `make coverage`
- `make integration-test`
- `make vuln`
- `make gosec`
- `make build`
- Docker image build
- Release workflow dry run or tag workflow validation

**Verification:**
- Final report includes exact passing outputs, remaining risks, and explicit merge/release recommendation.

---

## Kanban/delegation plan

- Assign corrected cards to the only discovered Hermes profile, `default`.
- Use `--workspace worktree` and branch names under `prod-ready-use-harbor/*` for corrected implementation cards; all such branches must be based on `origin/use-harbor`.
- Model review cards are read-only and can run independently.
- Synthesis cards are gated by their respective model review parents.
- Implementation cards are gated by synthesis outputs.
- Workers should report major progress in Kanban comments; this CLI session will send Telegram summaries when setting up and when manually asked for status.
- If dedicated reviewer/implementer profiles are later added, reassign cards before dispatching for better parallelism.


---

## Correction note: wrong initial base

The first planning and Kanban setup was accidentally based on `main` (`932411b`) rather than `origin/use-harbor` (`f910196`). Treat tenant `charm-registry-prod-ready` and branches `prod-ready/*` as superseded unless a human explicitly cherry-picks a specific result after reviewing it against `use-harbor`. The corrected tenant is `charm-registry-prod-ready-use-harbor`, and corrected branches use `prod-ready-use-harbor/*`.
