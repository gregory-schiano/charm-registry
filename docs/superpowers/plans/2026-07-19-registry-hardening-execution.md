# Registry Hardening Execution Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Execute the approved registry security, consistency, synchronization, and operations hardening as four review-gated, independently buildable slices.

**Architecture:** The implementation begins with the authorization boundary required by every later mutation, then introduces the artifact aggregate and cleanup contracts, builds synchronization on those contracts, and finishes with bounded read models and process lifecycle fixes. Each linked plan owns its exact files, tests, commits, and slice verification.

**Tech Stack:** Go 1.26.0 with toolchain 1.26.4, PostgreSQL/pgx/sqlc, SQLite/modernc, S3/filesystem blobs, CNCF Distribution OCI registry, Testify, Make.

## Global Constraints

- Follow the approved design in `docs/superpowers/specs/2026-07-19-registry-hardening-design.md`.
- Execute linked plans in the listed order because later interfaces consume earlier deliverables.
- Use test-driven development: observe each focused test fail before adding production behavior.
- Keep every task commit buildable and run its reviewer gate before starting the next task.
- Preserve unrelated user changes and do not perform opportunistic refactors.
- Regenerate SQL only with `go tool sqlc generate`; never hand-edit `internal/repo/db`.
- Prefer focused unit/backend tests; use integration suites only when unit scope cannot prove an invariant.
- Perform implementation in an isolated worktree created with `superpowers:using-git-worktrees`.

---

### Task 1: Establish a clean isolated execution baseline

**Files:**
- Read: `docs/superpowers/specs/2026-07-19-registry-hardening-design.md`
- Read: all four linked implementation plans below.
- Modify: none.

**Interfaces:**
- Consumes: committed design and plans.
- Produces: isolated branch/worktree and recorded baseline results.

- [ ] **Step 1: Create the isolated worktree**

Use `superpowers:using-git-worktrees`, choose branch `codex/registry-hardening`, and verify the worktree starts at the commit containing these plans.

Expected: the original workspace remains clean and the new worktree reports branch `codex/registry-hardening`.

- [ ] **Step 2: Verify the baseline**

Run in the worktree:

```bash
go test ./internal/... -count=1
go vet ./cmd/... ./internal/...
make build
go tool sqlc diff
```

Expected: all commands exit 0. If an unrelated baseline failure occurs, stop and report the exact command/output before changing production code.

- [ ] **Step 3: Record the starting point**

```bash
git status --short --branch
git log -1 --oneline
```

Expected: clean `codex/registry-hardening` branch at the plans commit.

### Task 2: Execute authorization and token hardening

**Files:**
- Plan: `docs/superpowers/plans/2026-07-19-authorization-token-hardening.md`

**Interfaces:**
- Consumes: baseline repository contracts.
- Produces: typed package roles, centralized package policies, safe token issuance/exchange, and OCI mount fallback.

- [ ] **Step 1: Execute every unchecked task in the authorization plan**

Follow [Authorization and Token Hardening Implementation Plan](./2026-07-19-authorization-token-hardening.md) in order, including each failing-test observation, commit, and final reviewer gate.

Expected: the slice's focused tests, race checks, sqlc diff, vet, and build pass before continuing.

### Task 3: Execute artifact lifecycle hardening

**Files:**
- Plan: `docs/superpowers/plans/2026-07-19-artifact-lifecycle-hardening.md`

**Interfaces:**
- Consumes: centralized package authorization from Task 2.
- Produces: owner-bound one-use uploads, package mutation locks, atomic publication/release/unregister, archive limits, and durable cleanup jobs.

- [ ] **Step 1: Execute every unchecked task in the artifact plan**

Follow [Artifact Lifecycle Hardening Implementation Plan](./2026-07-19-artifact-lifecycle-hardening.md) in order, including migrations, TDD cycles, commits, and reviewer gate.

Expected: both database implementations migrate, publication invariants pass, and the slice is buildable before continuing.

### Task 4: Execute Charmhub synchronization hardening

**Files:**
- Plan: `docs/superpowers/plans/2026-07-19-charmhub-sync-hardening.md`

**Interfaces:**
- Consumes: package transactions and cleanup jobs from Task 3.
- Produces: per-package leases, idempotent import stages, safe release pruning, and reference-aware OCI cleanup.

- [ ] **Step 1: Execute every unchecked task in the synchronization plan**

Follow [Charmhub Sync Hardening Implementation Plan](./2026-07-19-charmhub-sync-hardening.md) in order, including TDD cycles, backend-specific lease tests, commits, and reviewer gate.

Expected: concurrent managers cannot reconcile one package, retries repair incomplete imports, and pruning invariants pass before continuing.

### Task 5: Execute operations and performance hardening

**Files:**
- Plan: `docs/superpowers/plans/2026-07-19-operations-performance-hardening.md`

**Interfaces:**
- Consumes: final repository contracts from Tasks 2-4.
- Produces: bounded search, safe transaction cleanup, consistent OCI TLS defaults, graceful server/CLI lifecycle, and redirect downgrade protection.

- [ ] **Step 1: Execute every unchecked task in the operations plan**

Follow [Operations and Performance Hardening Implementation Plan](./2026-07-19-operations-performance-hardening.md) in order, including TDD cycles, commits, and reviewer gate.

Expected: search is bounded and constant-query, cancellation interrupts active requests, and normal shutdown exits successfully.

### Task 6: Final cross-slice verification and review

**Files:**
- Modify only when a failing verification exposes a defect within the approved scope.

**Interfaces:**
- Consumes: all four completed slices.
- Produces: verified branch ready for integration choice.

- [ ] **Step 1: Run formatting and generated-code checks**

```bash
make fmt
go tool sqlc diff
git diff --check
```

Expected: all commands exit 0 and formatting creates no unexpected scope.

- [ ] **Step 2: Run the complete unit and race suites**

```bash
make test
make test-race
```

Expected: PASS.

- [ ] **Step 3: Run static and build verification**

```bash
make tidy-check
make vet
make lint
make gosec
make build
```

Expected: all commands exit 0.

- [ ] **Step 4: Audit design coverage**

```bash
git log --oneline --decorate --reverse mvp..HEAD
git diff --stat mvp...HEAD
git status --short --branch
```

Expected: every plan task has a corresponding reviewed commit, only approved files changed, and the worktree is clean.

- [ ] **Step 5: Request a fresh broad code review**

Provide the reviewer with the design specification, four implementation plans, complete commit range, and verification output. Require explicit review of correctness, security boundaries, migration safety, transaction/external-effect ordering, concurrency, and performance.

Expected: no unresolved P0/P1/P2 findings.

- [ ] **Step 6: Use the branch-finishing workflow**

Invoke `superpowers:finishing-a-development-branch` and present its integration choices without merging, pushing, or deleting the worktree unless the user selects that action.

Expected: the verified branch remains intact pending the user's integration decision.
