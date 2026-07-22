# Artifact Lifecycle Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make uploads owner-bound and single-use, serialize package publication mutations, validate artifacts before persistence, and durably clean external artifacts without leaving partial package state.

**Architecture:** Uploads use conditional `uploading -> pending -> approved|rejected` transitions and deterministic object keys. A backend-specific package lock is acquired inside each affected database transaction; destructive blob/OCI effects are leased cleanup jobs committed with metadata changes. Hashing, archive parsing, descriptor parsing, and OCI calls stay outside package transactions.

**Tech Stack:** Go 1.26.0 with toolchain 1.26.4, PostgreSQL/pgx/sqlc, SQLite/modernc, CNCF Distribution, go-containerregistry, `archive/zip`, Testify.

## Global Constraints

- Preserve the public JSON shapes of existing upload, release, info, and download responses.
- Invalidate ownerless pending uploads as `rejected` with `legacy-upload-invalidated`; preserve approved history but never permit its reuse.
- New object keys are exactly `artifacts/<upload UUID>/payload`; client filenames remain metadata only.
- Terminal upload states never transition, and only one matching owner/package consumption succeeds.
- Reserve PostgreSQL migrations `0010_upload_lifecycle.sql` and `0011_cleanup_jobs.sql`; reserve SQLite migrations `0004_upload_lifecycle.sql` and `0005_cleanup_jobs.sql`.
- Call `PackageRepo.LockPackage` only inside `Transactor.WithinTransaction`.
- Keep parsing, hashing, blob reads/writes, and OCI network calls outside package transactions.
- Commit metadata deletion and cleanup-job enqueueing together; never perform external deletion in that transaction.
- Archive defaults are 10 MiB per relevant entry, 64 MiB cumulative relevant bytes, and 10,000 ZIP entries.
- OCI descriptors are limited to 1 MiB, contain exactly one supported JSON object and a valid digest, and reference an existing manifest in the exact package/resource repository.
- Empty release batches and duplicate release slots are invalid and write nothing.
- Do not hand-edit `internal/repo/db`; run `go tool sqlc generate`.
- Start each behavior with a focused failing unit test. Do not run integration suites unless unit/backend coverage cannot prove the invariant.
- Preserve unrelated worktree changes and avoid reorganizing untouched files.

---

## File map

- Create `internal/core/cleanup.go` for cleanup job values and deterministic constructors.
- Create `internal/charm/manifest.go` for reusable stored-metadata parsing.
- Create `internal/repo/queries/cleanup_jobs.sql`, `postgres_cleanup.go`, `sqlite_cleanup.go`, and `memory_cleanup.go`.
- Create `internal/cleanup/worker.go` and `worker_test.go`.
- Create the four migrations reserved above.
- Modify `internal/core/upload.go`, `constructors.go`, `resource.go`, and `oci_helpers.go`.
- Modify `internal/repo/interface.go`, package/revision/resource queries and adapters, `sqlite.go`, and `memory.go`.
- Modify `internal/service/service.go`, `revisions.go`, `resources.go`, `releases.go`, and `packages.go`.
- Modify `internal/service/oci_registry.go`, `internal/oci/client.go`, and `internal/testutil/oci_registry.go`.
- Modify `internal/charm/archive.go`, `internal/api/http_revisions.go`, and `internal/app/app.go`.
- Regenerate SQL only with sqlc.
- Extend the focused unit tests named in each task.

### Task 1: Owner-bound upload model, migrations, and conditional transitions

**Files:**
- Modify: `internal/core/upload.go`
- Modify: `internal/core/constructors.go`
- Test: `internal/core/constructors_test.go`
- Create: `internal/repo/migrations/0010_upload_lifecycle.sql`
- Create: `internal/repo/sqlite/migrations/0004_upload_lifecycle.sql`
- Modify: `internal/repo/interface.go`
- Modify: `internal/repo/queries/revisions.sql`
- Modify: `internal/repo/postgres_revisions.go`
- Modify: `internal/repo/postgres_sqlc.go`
- Modify: `internal/repo/sqlite.go`
- Modify: `internal/repo/memory.go`
- Test: `internal/repo/memory_test.go`
- Test: `internal/repo/sqlite_test.go`
- Test: `internal/repo/postgres_test.go`
- Regenerate: `internal/repo/db/revisions.sql.go`, `models.go`, `querier.go`

**Interfaces:**
- Consumes: `core.APIError`, `repo.ErrNotFound`, `repo.ErrConflict`.
- Produces: upload constants and owner/package fields; `core.NewUpload`; `ActivateUpload`, `RejectUpload`, and `ConsumeUpload`.

- [ ] **Step 1: Write failing core and backend transition tests**

Add `TestNewUploadRequiresOwnerAndDeterministicKey`:

~~~go
now := time.Now().UTC()
upload := core.Upload{
    ID: "27a30f75-c9f7-4f53-beb9-cc8065961854", Filename: "../../unsafe.charm",
    ObjectKey: "artifacts/27a30f75-c9f7-4f53-beb9-cc8065961854/payload",
    Size: 4, SHA256: "a", SHA384: "b", SHA512: "c",
    Status: core.UploadStatusUploading, Kind: core.UploadKindRevision,
    CreatedByAccountID: "acc-1", CreatedAt: now,
}
_, err := core.NewUpload(upload)
require.NoError(t, err)
upload.CreatedByAccountID = ""
_, err = core.NewUpload(upload)
require.ErrorContains(t, err, "upload creator account id is required")
~~~

For memory and migrated SQLite, insert one `uploading` row, activate it, prove a second activation conflicts, prove a stranger cannot consume it, consume it once as its creator/package, and prove a second consumption conflicts. Assert approved timestamp, revision, and `ConsumedByPackageID`.

- [ ] **Step 2: Run the tests to verify missing lifecycle APIs**

Run:

~~~bash
go test ./internal/core ./internal/repo -run 'Test(NewUpload|MemoryUploadLifecycle|SQLiteUploadLifecycle)' -count=1
~~~

Expected: FAIL to compile because constants, fields, constructor, and transition methods are absent.

- [ ] **Step 3: Add lifecycle values and constructor**

Use string constants to preserve JSON:

~~~go
const (
    UploadStatusUploading = "uploading"
    UploadStatusPending   = "pending"
    UploadStatusApproved  = "approved"
    UploadStatusRejected  = "rejected"
    UploadKindRevision    = "revision"
    UploadKindResource    = "resource"
)
~~~

Add hidden fields to `core.Upload`:

~~~go
CreatedByAccountID  string `json:"-"`
ConsumedByPackageID string `json:"-"`
~~~

`NewUpload(core.Upload) (core.Upload, error)` requires a UUID ID, nonempty filename/owner, key `artifacts/<ID>/payload`, nonnegative size, all three hashes, status `uploading`, one known kind, nonzero time, and no terminal fields.

- [ ] **Step 4: Add exact forward migrations**

PostgreSQL `0010_upload_lifecycle.sql`:

~~~sql
ALTER TABLE uploads
    ADD COLUMN IF NOT EXISTS consumed_by_package_id TEXT NULL
        REFERENCES packages(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_uploads_creator_status
    ON uploads (created_by_account_id, status);
CREATE INDEX IF NOT EXISTS idx_uploads_consumed_package_status
    ON uploads (consumed_by_package_id, status);

WITH refs AS (
  SELECT object_key, MIN(package_id) AS package_id
  FROM (
    SELECT object_key, package_id FROM revisions WHERE object_key <> ''
    UNION ALL
    SELECT rr.object_key, rd.package_id
    FROM resource_revisions rr
    JOIN resource_definitions rd ON rd.id = rr.resource_id
    WHERE rr.object_key <> ''
  ) all_refs
  GROUP BY object_key
  HAVING COUNT(DISTINCT package_id) = 1
)
UPDATE uploads u
SET consumed_by_package_id = refs.package_id
FROM refs
WHERE u.status = 'approved'
  AND u.consumed_by_package_id IS NULL
  AND u.object_key = refs.object_key;

UPDATE uploads
SET status = 'rejected',
    approved_at = COALESCE(approved_at, NOW()),
    errors = '[{"code":"legacy-upload-invalidated","message":"legacy ownerless pending upload must be uploaded again"}]'::jsonb
WHERE status = 'pending'
  AND (created_by_account_id IS NULL OR created_by_account_id = '');
~~~

SQLite `0004_upload_lifecycle.sql` is the exact forward migration below:

~~~sql
ALTER TABLE uploads ADD COLUMN created_by_account_id TEXT NULL;
ALTER TABLE uploads ADD COLUMN consumed_by_package_id TEXT NULL
    REFERENCES packages(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_uploads_creator_status
    ON uploads (created_by_account_id, status);
CREATE INDEX IF NOT EXISTS idx_uploads_consumed_package_status
    ON uploads (consumed_by_package_id, status);

UPDATE uploads
SET consumed_by_package_id = (
  SELECT MIN(package_id)
  FROM (
    SELECT package_id FROM revisions WHERE object_key = uploads.object_key
    UNION ALL
    SELECT rd.package_id
    FROM resource_revisions rr
    JOIN resource_definitions rd ON rd.id = rr.resource_id
    WHERE rr.object_key = uploads.object_key
  ) refs
  HAVING COUNT(DISTINCT package_id) = 1
)
WHERE status = 'approved'
  AND consumed_by_package_id IS NULL;

UPDATE uploads
SET status = 'rejected',
    approved_at = COALESCE(approved_at, CURRENT_TIMESTAMP),
    errors = '[{"code":"legacy-upload-invalidated","message":"legacy ownerless pending upload must be uploaded again"}]'
WHERE status = 'pending'
  AND (created_by_account_id IS NULL OR created_by_account_id = '');
~~~

Add a migration test using a minimal pre-0004 schema: ownerless pending becomes rejected; an unambiguous approved reference is associated; a key shared by two packages stays unassociated.

- [ ] **Step 5: Replace unconditional approval with conditional transitions**

Expose:

~~~go
ActivateUpload(ctx context.Context, uploadID, accountID string) error
RejectUpload(ctx context.Context, uploadID, accountID string, reviewedAt time.Time, errors []core.APIError) error
ConsumeUpload(ctx context.Context, uploadID, accountID, packageID string, revision int, approvedAt time.Time) error
~~~

PostgreSQL predicates:

~~~sql
UPDATE uploads SET status = 'pending'
WHERE id = sqlc.arg(upload_id)
  AND created_by_account_id = sqlc.arg(account_id)
  AND status = 'uploading';

UPDATE uploads
SET status = 'rejected', approved_at = sqlc.arg(reviewed_at), errors = sqlc.arg(errors)
WHERE id = sqlc.arg(upload_id)
  AND created_by_account_id = sqlc.arg(account_id)
  AND status IN ('uploading', 'pending');

UPDATE uploads
SET status = 'approved', approved_at = sqlc.arg(approved_at),
    revision = sqlc.arg(revision), consumed_by_package_id = sqlc.arg(package_id),
    errors = '[]'::jsonb
WHERE id = sqlc.arg(upload_id)
  AND created_by_account_id = sqlc.arg(account_id)
  AND created_by_account_id <> ''
  AND status = 'pending'
  AND consumed_by_package_id IS NULL;
~~~

Mirror predicates in SQLite and memory. A zero-row transition performs an existence check: absent ID returns `ErrNotFound`; any owner/state/package mismatch returns `ErrConflict`. Include owner/package columns in create/select/scanners. Retain the existing `ApproveUpload` contract and implementation in this transitional commit because revision and resource publication still call it; Task 7 removes it after Tasks 4, 6, and 7 migrate every production caller and test double.

- [ ] **Step 6: Generate, test, and commit**

~~~bash
go tool sqlc generate
go tool sqlc diff
go test ./internal/core ./internal/repo -run 'Test(NewUpload|MemoryUploadLifecycle|SQLiteUploadLifecycle)' -count=1
git add internal/core internal/repo
git commit -m "feat: enforce owner-bound upload lifecycle"
~~~

Expected: sqlc diff exits 0 and named tests PASS.

### Task 2: Locked package transactions and constant-time allocation reads

**Files:**
- Modify: `internal/repo/interface.go`
- Modify: `internal/repo/queries/packages.sql`
- Modify: `internal/repo/queries/resources.sql`
- Modify: `internal/repo/postgres_packages.go`
- Modify: `internal/repo/postgres_resources.go`
- Modify: `internal/repo/postgres_sqlc.go`
- Modify: `internal/repo/sqlite.go`
- Modify: `internal/repo/memory.go`
- Create: `internal/repo/memory_transaction.go`
- Test: `internal/repo/postgres_test.go`
- Test: `internal/repo/sqlite_test.go`
- Test: `internal/repo/memory_test.go`
- Modify: `internal/service/service.go`
- Regenerate: package/resource generated SQL

**Interfaces:**
- Consumes: `Transactor.WithinTransaction`.
- Produces: `LockPackage`, `GetLatestResourceRevision`, rollback-capable memory transaction snapshots, and `withLockedPackageTransaction`.

- [ ] **Step 1: Add failing lock and latest-resource tests**

Inside a SQLite transaction, call `LockPackage` for a real row and assert a missing row maps to `ErrNotFound`. Insert resource revisions 1 and 3 and assert latest is 3 in SQLite and memory.

Add `TestMemoryWithinTransactionRollsBackOnErrorAndPanic`. Seed a package plus an owner-bound pending upload. In one callback consume the upload and delete the package, then return `assert.AnError`; assert both records retain their original state. Repeat with a panic, recover in the test, and assert the same rollback. Add a success case and assert both changes become visible together after the callback.

- [ ] **Step 2: Run and confirm missing methods**

~~~bash
go test ./internal/repo -run 'Test(SQLiteLockPackage|LatestResourceRevision|MemoryWithinTransaction)' -count=1
~~~

Expected: lock/latest tests FAIL to compile, and the memory rollback case fails against the current live-map callback.

- [ ] **Step 3: Implement backend locks**

Add `GetPackageByIDForUpdate` selecting the exact `GetPackageByID` projection and ending:

~~~sql
WHERE p.id = $1
FOR UPDATE OF p;
~~~

PostgreSQL converts that row through `packageFromParts`. SQLite:

~~~go
func (s *SQLite) LockPackage(ctx context.Context, packageID string) (core.Package, error) {
    res, err := s.db.ExecContext(ctx,
        "UPDATE packages SET updated_at = updated_at WHERE id = ?", packageID)
    if err := rowsErr(res, err); err != nil {
        return core.Package{}, err
    }
    return s.GetPackageByID(ctx, packageID)
}
~~~

Memory transactions use an isolated deep snapshot rather than mutating live maps. Create `memory_transaction.go` with `cloneMemoryUnlocked` and `replaceMemoryStateUnlocked` helpers. `WithinTransaction` locks the original `mu` for the complete callback, deep-clones all repository state into a new `*Memory` with independent mutexes, and passes only that transaction view to the callback. On callback error, context cancellation, or panic, it discards the view; the original is untouched and the panic continues unwinding. On success, it deep-clones the view once more and swaps every state map into the original while the original lock is still held. This also blocks ordinary readers/writers from observing partial state without requiring every method to acquire a second mutex.

The snapshot at this task includes accounts, account IDs, tokens (including slice/pointer fields), packages (including maps/slices/robot pointers), group membership, ACLs, uploads, revisions, resource definitions/revisions, releases, and sync rules. Use explicit domain clone helpers plus `maps.Clone`/`slices.Clone`; do not use JSON round-tripping because token hashes and robot secrets have `json:"-"` fields. Task 3 extends the snapshot with cleanup jobs when that map is introduced. Sync-lease holder maps remain outside this state because `SyncLeaseRepo` is not part of `CompositeRepo`.

`Memory.LockPackage` calls `GetPackageByID` on the transaction view. Add a test-only assertion that mutating a nested map/slice through a failed transaction cannot alias back into the original.

- [ ] **Step 4: Add latest resource reads**

Add and implement:

~~~go
GetLatestResourceRevision(ctx context.Context, resourceID string) (core.ResourceRevision, error)
~~~

SQL orders by `revision DESC LIMIT 1`; all backends return `ErrNotFound` for none.

- [ ] **Step 5: Add the service helper**

~~~go
func (s *Service) withLockedPackageTransaction(
    ctx context.Context,
    packageID string,
    fn func(repo.CompositeRepo, core.Package) error,
) error {
    if err := s.tx.WithinTransaction(ctx, func(repository repo.CompositeRepo) error {
        pkg, err := repository.LockPackage(ctx, packageID)
        if err != nil {
            return err
        }
        return fn(repository, pkg)
    }); err != nil {
        return fmt.Errorf("cannot complete locked package transaction: %w", err)
    }
    return nil
}
~~~

Keep `withRepositoryTransaction` for registration, which has no existing package row.

- [ ] **Step 6: Generate, test, and commit**

~~~bash
go tool sqlc generate
go tool sqlc diff
go test ./internal/repo ./internal/service -run 'Test(SQLiteLockPackage|LatestResourceRevision|MemoryWithinTransaction)' -count=1
git add internal/repo internal/service/service.go
git commit -m "feat: add locked package transaction boundary"
~~~

Expected: all commands exit 0.

### Task 3: Durable leased cleanup jobs

**Files:**
- Create: `internal/core/cleanup.go`
- Create: `internal/repo/migrations/0011_cleanup_jobs.sql`
- Create: `internal/repo/sqlite/migrations/0005_cleanup_jobs.sql`
- Create: `internal/repo/queries/cleanup_jobs.sql`
- Create: `internal/repo/postgres_cleanup.go`
- Create: `internal/repo/sqlite_cleanup.go`
- Create: `internal/repo/memory_cleanup.go`
- Create: `internal/cleanup/worker.go`
- Test: `internal/cleanup/worker_test.go`
- Modify: `internal/repo/interface.go`
- Modify: `internal/repo/memory.go`
- Modify: `internal/repo/postgres_sqlc.go`
- Test: `internal/repo/memory_test.go`
- Test: `internal/repo/sqlite_test.go`
- Modify: `internal/app/app.go`
- Test: `internal/app/app_test.go`
- Regenerate: cleanup/model/querier generated SQL

**Interfaces:**
- Consumes: `blob.Store`, OCI deletion, package lookup.
- Produces: the exact cleanup contract consumed by the sync slice.

- [ ] **Step 1: Add failing constructor, dedupe, and lease tests**

Construct the same OCI target under two IDs and assert equal dedupe keys. For memory and SQLite, enqueue twice, claim with worker A, prove B cannot claim before expiry, prove B can claim after expiry, and prove only the current claimant can complete/fail.

- [ ] **Step 2: Run and verify missing APIs**

~~~bash
go test ./internal/core ./internal/repo -run 'Test(CleanupJob|MemoryCleanup|SQLiteCleanup)' -count=1
~~~

Expected: FAIL to compile.

- [ ] **Step 3: Add exact domain types**

Create `internal/core/cleanup.go`:

~~~go
type CleanupJobKind string

const (
    CleanupJobBlobObject       CleanupJobKind = "blob-object"
    CleanupJobOCIImageManifest CleanupJobKind = "oci-image-manifest"
    CleanupJobOCIProject       CleanupJobKind = "oci-project"
)

type CleanupTarget struct {
    ObjectKey, PackageID, PackageName, OCIProject, ResourceName, Digest string
}

type CleanupJob struct {
    ID string
    Kind CleanupJobKind
    DedupeKey string
    Target CleanupTarget
    Attempts int
    NextAttemptAt time.Time
    LastError, ClaimedBy string
    ClaimExpiresAt, CompletedAt *time.Time
    CreatedAt time.Time
}
~~~

Implement:

~~~go
func NewBlobCleanupJob(id, objectKey string, now time.Time) (CleanupJob, error)
func NewOCIImageCleanupJob(id, packageID, packageName, ociProject, resourceName, digest string, now time.Time) (CleanupJob, error)
func NewOCIProjectCleanupJob(id, packageName, ociProject string, now time.Time) (CleanupJob, error)
~~~

Validate required target fields. Compute dedupe key as lowercase hex SHA-256 over `string(kind)+"\n"+json.Marshal(target)`. Initialize `NextAttemptAt` and `CreatedAt` to `now`.

- [ ] **Step 4: Add cleanup schemas and atomic claim query**

PostgreSQL `0011_cleanup_jobs.sql` uses this exact schema:

~~~sql
CREATE TABLE cleanup_jobs (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  dedupe_key TEXT NOT NULL,
  target JSONB NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 0,
  next_attempt_at TIMESTAMPTZ NOT NULL,
  last_error TEXT NOT NULL DEFAULT '',
  claimed_by TEXT NOT NULL DEFAULT '',
  claim_expires_at TIMESTAMPTZ NULL,
  created_at TIMESTAMPTZ NOT NULL,
  completed_at TIMESTAMPTZ NULL
);
CREATE UNIQUE INDEX cleanup_jobs_active_target_idx
  ON cleanup_jobs (kind, dedupe_key) WHERE completed_at IS NULL;
CREATE INDEX cleanup_jobs_due_idx
  ON cleanup_jobs (next_attempt_at, claim_expires_at) WHERE completed_at IS NULL;
~~~

SQLite `0005_cleanup_jobs.sql` is explicit rather than inferred from PostgreSQL:

~~~sql
CREATE TABLE cleanup_jobs (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  dedupe_key TEXT NOT NULL,
  target TEXT NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 0,
  next_attempt_at TIMESTAMP NOT NULL,
  last_error TEXT NOT NULL DEFAULT '',
  claimed_by TEXT NOT NULL DEFAULT '',
  claim_expires_at TIMESTAMP NULL,
  created_at TIMESTAMP NOT NULL,
  completed_at TIMESTAMP NULL
);
CREATE UNIQUE INDEX cleanup_jobs_active_target_idx
  ON cleanup_jobs (kind, dedupe_key) WHERE completed_at IS NULL;
CREATE INDEX cleanup_jobs_due_idx
  ON cleanup_jobs (next_attempt_at, claim_expires_at) WHERE completed_at IS NULL;
~~~

PostgreSQL claim:

~~~sql
WITH candidates AS (
  SELECT id FROM cleanup_jobs
  WHERE completed_at IS NULL
    AND next_attempt_at <= sqlc.arg(now)
    AND (claim_expires_at IS NULL OR claim_expires_at <= sqlc.arg(now))
  ORDER BY next_attempt_at, created_at, id
  FOR UPDATE SKIP LOCKED
  LIMIT sqlc.arg(batch_limit)
)
UPDATE cleanup_jobs jobs
SET claimed_by = sqlc.arg(worker_id),
    claim_expires_at = sqlc.arg(claim_until),
    attempts = jobs.attempts + 1
FROM candidates
WHERE jobs.id = candidates.id
RETURNING jobs.*;
~~~

Add active-target enqueue with `ON CONFLICT (kind, dedupe_key) WHERE completed_at IS NULL DO NOTHING`, claimant-guarded complete/fail, `BlobObjectKeyReferenced` over revision/resource object keys, and `OCIImageDigestReferenced` joining definitions by package/name/digest. SQLite claims atomically with this one statement:

~~~sql
UPDATE cleanup_jobs
SET claimed_by = ?, claim_expires_at = ?, attempts = attempts + 1
WHERE id IN (
  SELECT id FROM cleanup_jobs
  WHERE completed_at IS NULL
    AND next_attempt_at <= ?
    AND (claim_expires_at IS NULL OR claim_expires_at <= ?)
  ORDER BY next_attempt_at, created_at, id
  LIMIT ?
)
RETURNING id, kind, dedupe_key, target, attempts, next_attempt_at,
  last_error, claimed_by, claim_expires_at, created_at, completed_at;
~~~

The three PostgreSQL state changes use these predicates; the SQLite statements use `?` parameters with identical predicates:

~~~sql
INSERT INTO cleanup_jobs (id, kind, dedupe_key, target, attempts, next_attempt_at,
  last_error, claimed_by, claim_expires_at, created_at, completed_at)
VALUES ($1, $2, $3, $4, 0, $5, '', '', NULL, $6, NULL)
ON CONFLICT (kind, dedupe_key) WHERE completed_at IS NULL DO NOTHING;

UPDATE cleanup_jobs
SET completed_at = $3, claimed_by = '', claim_expires_at = NULL
WHERE id = $1 AND claimed_by = $2 AND completed_at IS NULL;

UPDATE cleanup_jobs
SET last_error = $3, next_attempt_at = $4,
    claimed_by = '', claim_expires_at = NULL
WHERE id = $1 AND claimed_by = $2 AND completed_at IS NULL;
~~~

- [ ] **Step 5: Expose the shared repository interface**

~~~go
type CleanupClaim struct {
    WorkerID string
    Now, ClaimUntil time.Time
    Limit int
}

type CleanupRepo interface {
    EnqueueCleanupJob(ctx context.Context, job core.CleanupJob) error
    ClaimCleanupJobs(ctx context.Context, claim CleanupClaim) ([]core.CleanupJob, error)
    CompleteCleanupJob(ctx context.Context, jobID, workerID string, completedAt time.Time) error
    FailCleanupJob(ctx context.Context, jobID, workerID, lastError string, nextAttemptAt time.Time) error
    BlobObjectKeyReferenced(ctx context.Context, objectKey string) (bool, error)
    OCIImageDigestReferenced(ctx context.Context, packageID, resourceName, digest string) (bool, error)
}
~~~

Embed `CleanupRepo` in `CompositeRepo`. Zero-row claimant updates return `ErrConflict`.

Add the in-memory cleanup-job map to `cloneMemoryUnlocked` and `replaceMemoryStateUnlocked`, deep-cloning job target/time pointer fields. Extend `TestMemoryWithinTransactionRollsBackOnErrorAndPanic` with an enqueued cleanup job followed by `assert.AnError`; the job must not be claimable after rollback, while the success variant becomes claimable only after commit.

- [ ] **Step 6: Add failing worker tests**

With a fixed clock and no sleeps, verify: unreferenced blob deletes/completes; referenced blob and OCI manifest complete without deletion; deletion failure records error and future retry; expired claims recover; missing package makes OCI-image cleanup a completed no-op; OCI-project cleanup works after package deletion.

- [ ] **Step 7: Implement and wire worker**

~~~go
type OCIRegistry interface {
    DeleteImage(context.Context, core.Package, string, string) error
    DeletePackage(context.Context, core.Package) error
}
func New(repository repo.Backend, blobs blob.Store, oci OCIRegistry) *Worker
func (w *Worker) Start(context.Context) io.Closer
func (w *Worker) RunOnce(context.Context) error
func (w *Worker) Close() error
~~~

Defaults: poll 5 seconds, claim TTL 30 seconds, batch 32. Claim increments attempts. Retry after `min(2^(attempts-1) seconds, 15 minutes)`, implemented without an unbounded shift: attempts 1 through 10 use `time.Second << (attempts-1)` and attempts 11 or greater use 15 minutes. Recheck references immediately before delete. In `app.New`, start cleanup before sync and append its closer before the sync-manager closer, so reverse shutdown stops sync then cleanup before dependencies.

- [ ] **Step 8: Generate, test, and commit**

~~~bash
go tool sqlc generate
go tool sqlc diff
go test ./internal/core ./internal/repo ./internal/cleanup ./internal/app -run 'Test(Cleanup|MemoryCleanup|SQLiteCleanup)' -count=1
git add internal/core/cleanup.go internal/repo internal/cleanup internal/app
git commit -m "feat: add durable external cleanup jobs"
~~~

Expected: all commands exit 0.

### Task 4: Identity-aware creation, compensation, review scoping, and abandonment

**Files:**
- Modify: `internal/service/revisions.go`
- Test: `internal/service/service_test.go`
- Modify: `internal/api/http_revisions.go`
- Test: `internal/api/http_test.go`

**Interfaces:**
- Consumes: lifecycle transitions and cleanup enqueue.
- Produces: identity-taking upload methods, staged/finalized multipart methods, and `AbandonUpload`.

- [ ] **Step 1: Add failing ownership/compensation/review tests**

Assert a client filename `../../client.charm` produces key `artifacts/<ID>/payload`, owner ID persists, and status is pending. Inject blob-put and activation errors; assert no usable row remains and deterministic cleanup is attempted/queued. Assert strangers cannot review pending/rejected rows, approved rows are visible only through their consumed package, and administrators can inspect unassociated terminal history.

- [ ] **Step 2: Run and verify failures**

~~~bash
go test ./internal/service -run 'Test(CreateUploadBindsOwner|CreateUploadCompensates|ReviewUploadScopes)' -count=1
~~~

Expected: FAIL because identity/key/scoping behavior is absent.

- [ ] **Step 3: Implement exact creation sequence**

Change signatures:

~~~go
func (s *Service) CreateUpload(ctx context.Context, identity core.Identity, filename string, payload []byte) (core.Upload, error)
func (s *Service) CreateUploadStream(ctx context.Context, identity core.Identity, filename string, payload io.Reader) (core.Upload, error)
func (s *Service) StageUploadStream(ctx context.Context, identity core.Identity, filename string, payload io.Reader) (core.Upload, error)
func (s *Service) FinalizeUpload(ctx context.Context, identity core.Identity, uploadID string) (core.Upload, error)
~~~

`StageUploadStream` and `FinalizeUpload` both call the existing `AuthorizeUpload(identity)` guard, which requires `permAccountRegisterPackage`; they do not invent a package selector before a package is known. Stage hashes/counts into a temporary seekable file, inserts a validated `uploading` row, and puts `artifacts/<UUID>/payload` without making it consumable. `FinalizeUpload` conditionally activates that caller's row and returns status pending. `CreateUploadStream` calls stage then finalize for non-multipart/internal callers. On put/finalization error, use `context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)` to reject and enqueue blob cleanup in one repository transaction, then make one immediate best-effort delete.

- [ ] **Step 4: Add abandonment and scoped review**

~~~go
func (s *Service) AbandonUpload(ctx context.Context, identity core.Identity, uploadID string) error {
    if err := s.requirePermission(identity, permAccountRegisterPackage); err != nil {
        return err
    }
    return s.rejectAndScheduleUpload(ctx, uploadID, identity.Account.ID, []core.APIError{{
        Code: "upload-abandoned", Message: "multipart upload was abandoned",
    }})
}
~~~

Review visibility: uploading/pending/rejected require matching nonempty creator; approved requires matching nonempty consumed package; system/admin bypass. Return the existing upload-not-found response for a mismatch.

- [ ] **Step 5: Update every caller with the authenticated identity**

Pass handler identity to `CreateUploadStream`. In `service_test.go`, pass the same `owner` later used to push. Add `owner := newIdentity("upload-owner","upload-owner")` in the seekable, kind, and hash-only tests. Pass the resolved SQLite owner in rollback tests. Use the compiler to confirm no three-argument service upload call remains:

~~~bash
rg -n 'svc\.CreateUpload(Stream)?\(ctx, "[^"]+"' internal --glob '*.go'
~~~

Expected: no matches.

- [ ] **Step 6: Test and commit**

~~~bash
go test ./internal/service ./internal/api -run 'Test(CreateUpload|ReviewUpload|UnscannedUpload)' -count=1
git add internal/service/revisions.go internal/service/service_test.go internal/api/http_revisions.go internal/api/http_test.go
git commit -m "feat: bind uploads to authenticated creators"
~~~

Expected: tests PASS.

### Task 5: Bounded archive parsing and reusable stored-metadata parsing

**Files:**
- Create: `internal/charm/manifest.go`
- Modify: `internal/charm/archive.go`
- Test: `internal/charm/archive_test.go`
- Test: `internal/charm/archive_fuzz_test.go`
- Modify: `internal/service/revisions.go`

**Interfaces:**
- Consumes: `core.CharmManifest`.
- Produces: `ArchiveLimits`, limit-aware parse functions, and `ParseMetadata` for sync repair.

- [ ] **Step 1: Add failing safety tests**

Use ordered ZIP entries to test case-insensitive duplicates, custom cumulative overflow, custom entry-count overflow, a large irrelevant entry that is never opened, malformed `manifest.yaml`, exact-boundary success, and container-derived resources from `ParseMetadata`.

- [ ] **Step 2: Verify current failures**

~~~bash
go test ./internal/charm -run 'TestParseArchive(RejectsDuplicate|SkipsIrrelevant|RejectsCumulative|RejectsEntryCount|InvalidManifest)|TestParseMetadata' -count=1
~~~

Expected: duplicate/cumulative/count/manifest tests FAIL.

- [ ] **Step 3: Add exact limits and metadata API**

~~~go
type ArchiveLimits struct {
    MaxEntryBytes int64
    MaxTotalBytes int64
    MaxEntries int
}
func DefaultArchiveLimits() ArchiveLimits {
    return ArchiveLimits{10 << 20, 64 << 20, 10_000}
}
func ParseArchiveWithLimits([]byte, ArchiveLimits) (core.CharmArchive, error)
func ParseArchiveFileWithLimits(string, int64, ArchiveLimits) (core.CharmArchive, error)
func ParseMetadata(metadataYAML string) (core.CharmManifest, error)
~~~

`ParseMetadata` unmarshals metadata and propagates YAML syntax/type errors, wraps them with `parse metadata.yaml`, calls `populateContainerResources`, and returns. Do not enable YAML `KnownFields`: charm metadata is extensible and unknown valid fields must remain compatible. Keep existing public parse functions as wrappers.

`ParseArchiveWithLimits` and `ParseArchiveFileWithLimits` reject nonpositive limit fields with an `invalid archive limits` error. Existing wrappers always pass `DefaultArchiveLimits`; service configuration overrides only the positive per-entry value.

- [ ] **Step 4: Filter before decompressing**

Reject excessive `len(reader.File)` first. Canonicalize only root `metadata.yaml`, `manifest.yaml`, `config.yaml`, `actions.yaml`, `bundle.yaml`, `readme.md`; skip all others before `Open`. Reject duplicate canonical names. Limit reads by both per-entry and remaining cumulative budget and count actual bytes. Strictly unmarshal present manifest bases and return `parse manifest.yaml` errors.

- [ ] **Step 5: Pass explicit service limits**

Start from defaults, override only `MaxEntryBytes` when `cfg.MaxArchiveFileBytes > 0`, and call `ParseArchiveFileWithLimits` for local and temporary blobs.

- [ ] **Step 6: Test/fuzz and commit**

~~~bash
go test ./internal/charm ./internal/service -run 'TestParseArchive|TestParseMetadata|TestPushRevisionInvalidArchive' -count=1
go test ./internal/charm -run '^$' -fuzz '^FuzzParseArchive$' -fuzztime=5s
git add internal/charm internal/service/revisions.go
git commit -m "fix: bound charm archive decompression"
~~~

Expected: tests and short fuzz run succeed.

### Task 6: Atomic single-consumer revision publication

**Files:**
- Modify: `internal/service/revisions.go`
- Test: `internal/service/service_test.go`
- Test: `internal/repo/postgres_test.go`
- Test: `internal/repo/sqlite_test.go`

**Interfaces:**
- Consumes: package lock, `ConsumeUpload`, latest revision, validated resource definitions.
- Produces: atomic revision/package/resource-definition commit.

- [ ] **Step 1: Add failing behavior tests**

Add SQLite-backed tests for another account consuming an upload, reuse across two managed packages, two concurrent pushes allocating `[1,2]`, and an injected post-consumption write failure leaving the upload pending with no revision/definitions/package update. Run the post-consumption rollback invariant against Memory as well, asserting its transaction snapshot restores the upload and every nested package/resource value.

- [ ] **Step 2: Run and observe unsafe behavior**

~~~bash
go test ./internal/service -run 'TestPushRevision(RejectsAnother|CannotReuse|ConcurrentAllocation|FailureLeaves)' -count=1
~~~

Expected: ownership/reuse fail; concurrent allocation may conflict.

- [ ] **Step 3: Prepare immutable draft outside transaction**

Load the upload first and require `pending`, a nonempty creator, and `CreatedByAccountID == identity.Account.ID` before opening its blob. Then parse/archive-validate and build a draft containing revision metadata, validated resource definitions, and package metadata changes, but no revision number/version. Invalid archive/resource declarations call `rejectAndScheduleUpload` with `invalid-archive`; an ownership/state mismatch returns `upload-not-consumable` without reading or rejecting the other account's artifact.

- [ ] **Step 4: Commit under the lock**

Inside `withLockedPackageTransaction`: get latest and allocate; consume upload by creator/package/revision; create revision; upsert every draft definition; apply metadata to the locked package; update it. Any error rolls back all records and consumption. Map consumption conflict to `upload-not-consumable`. Emit logs only after success.

Representative order:

~~~go
latest, err := repository.GetLatestRevision(ctx, locked.ID)
revisionNumber := 1
if err == nil { revisionNumber = latest.Revision + 1 } else if !errors.Is(err, repo.ErrNotFound) { return err }
if err := repository.ConsumeUpload(ctx, upload.ID, identity.Account.ID, locked.ID, revisionNumber, now); err != nil { return err }
if err := repository.CreateRevision(ctx, revision); err != nil { return err }
// upsert definitions, then update locked package
~~~

- [ ] **Step 5: Update repository rollback tests**

Replace old approval calls with `ConsumeUpload`; include owner/package fields. Assert rollback restores pending, empty consumed package, nil approved/revision, and no revision/definitions.

- [ ] **Step 6: Test/race and commit**

~~~bash
go test ./internal/service ./internal/repo -run 'TestPushRevision|TestRepositoryPushRevisionStyleTransaction' -count=1
go test -race ./internal/service -run 'TestPushRevisionConcurrentAllocation' -count=1
git add internal/service/revisions.go internal/service/service_test.go internal/repo/postgres_test.go internal/repo/sqlite_test.go
git commit -m "fix: publish revisions atomically"
~~~

Expected: PASS with distinct allocations.

### Task 7: Atomic resource publication and OCI manifest verification

**Files:**
- Modify: `internal/core/resource.go`
- Modify: `internal/core/constructors.go`
- Modify: `internal/core/oci_helpers.go`
- Test: `internal/core/constructors_test.go`
- Modify: `internal/service/resources.go`
- Modify: `internal/service/oci_registry.go`
- Test: `internal/service/service_test.go`
- Modify: `internal/oci/client.go`
- Test: `internal/oci/client_test.go`
- Modify: `internal/testutil/oci_registry.go`
- Test: `internal/api/http_test.go`
- Modify: `internal/repo/interface.go`
- Modify: `internal/repo/queries/revisions.sql`
- Modify: `internal/repo/postgres_revisions.go`
- Modify: `internal/repo/sqlite.go`
- Modify: `internal/repo/memory.go`
- Test: `internal/repo/memory_test.go`
- Regenerate: `internal/repo/db/revisions.sql.go`, `querier.go`
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Consumes: package lock, latest resource, upload consumption, cleanup enqueue.
- Produces: resource/digest validation and `VerifyImageManifest`.

- [ ] **Step 1: Add failing validation/atomicity tests**

Test only `file`/`oci-image`; omitted request type; mismatched type; empty/malformed digest; trailing/unknown descriptor JSON; >1 MiB descriptor; HEAD failure with no mutation; concurrent allocations `[1,2]`; rollback after consumption; successful OCI descriptor cleanup enqueue.

Use:

~~~go
const testOCIDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
~~~

Define the same valid digest fixture in the API test package and replace every successful service/API OCI fixture that currently uses `sha256:test`, `sha256:deadbeef`, or `sha256:abc123`. Keep malformed values only in tests that explicitly assert rejection. Run `rg -n 'sha256:(test|deadbeef|abc123)' internal/service/service_test.go internal/api/http_test.go` and review every remaining match before the task commit.

- [ ] **Step 2: Run and verify failures**

~~~bash
go test ./internal/core ./internal/service ./internal/oci -run 'Test(NewResourceDefinition|PushResource|VerifyImageManifest|ValidateOCIDigest)' -count=1
~~~

Expected: unsafe cases FAIL.

- [ ] **Step 3: Validate domain values**

Add `ResourceTypeFile`, `ResourceTypeOCIImage`, and `ValidateResourceType`; call it from `NewResourceDefinition`. Import `github.com/opencontainers/go-digest` directly:

~~~go
func ValidateOCIDigest(value string) error {
    if _, err := digest.Parse(value); err != nil {
        return fmt.Errorf("invalid OCI image digest %q: %w", value, err)
    }
    return nil
}
~~~

`renderOCIImageBlob` rejects empty/invalid digest.

- [ ] **Step 4: Decode exactly one bounded descriptor**

Open/limit to `1<<20 + 1`, reject overflow, use `json.Decoder.DisallowUnknownFields`, decode the existing fields `ImageName`, `RegistryPath`, `Username`, `Password`, lowercase `username/password`, and `Digest`; require a second decode to return `io.EOF`; validate digest.

- [ ] **Step 5: Add authenticated HEAD**

Extend:

~~~go
VerifyImageManifest(ctx context.Context, pkg core.Package, resourceName, digest string) error
~~~

`oci.Client` validates digest, gets push credentials, constructs the exact internal `<host>/<project>/<sanitized-resource>@<digest>` reference, and calls `remote.Head` with configured transport/context/auth. Wrap failures as `cannot verify OCI image manifest`. Testutil returns nil; failing doubles forward or inject errors.

- [ ] **Step 6: Prepare then commit `PushResource`**

Outside transaction: authorize, load/validate definition and owned pending upload, use authoritative definition type, reject nonempty mismatch, validate optional package revision, and decode+HEAD OCI descriptor. Inside lock: re-fetch definition/package revision; allocate via latest query; consume; create revision; enqueue descriptor blob cleanup for OCI. File resources adopt upload key; OCI resources store empty key plus digest. Log after commit.

- [ ] **Step 7: Make resource metadata updates atomic**

Under one package lock, load every requested revision into a slice before writing. Then update all. Missing/invalid second entry rolls back and returns zero.

- [ ] **Step 8: Remove the transitional unconditional approval API**

After `PushRevision`, invalid review, and `PushResource` all use `RejectUpload` or `ConsumeUpload`, run:

~~~bash
rg -n 'ApproveUpload\(' internal --glob '*.go' --glob '!internal/repo/db/**'
~~~

Migrate the remaining memory repository tests and the `approveUploadFailingRepository` service test double to the conditional lifecycle methods. Then remove `ApproveUpload` from `PackageRepo`, all three backends, and `internal/repo/queries/revisions.sql`; regenerate sqlc. The same task must leave no non-generated caller, so this commit remains buildable.

- [ ] **Step 9: Tidy, test/race, and commit**

~~~bash
go mod tidy
go mod verify
go tool sqlc generate
go tool sqlc diff
go test ./internal/core ./internal/repo ./internal/service ./internal/oci ./internal/api -run 'Test(NewResourceDefinition|UploadLifecycle|PushResource|UpdateResourceRevisions|VerifyImageManifest|ValidateOCIDigest|OCI)' -count=1
go test -race ./internal/service -run 'TestPushResourceConcurrentAllocation' -count=1
git add go.mod go.sum internal/core internal/repo internal/service internal/oci internal/api/http_test.go internal/testutil/oci_registry.go
git commit -m "fix: validate and atomically publish resources"
~~~

Expected: all commands exit 0.

### Task 8: Atomic release, track, and resource-metadata batches

**Files:**
- Modify: `internal/service/releases.go`
- Modify: `internal/service/resources.go`
- Test: `internal/service/service_test.go`
- Test: `internal/api/http_test.go`

**Interfaces:**
- Consumes: locked transaction and transaction-bound repository reads.
- Produces: validate-all/write-all batch behavior.

- [ ] **Step 1: Add failing batch tests**

Test empty releases keep package registered; valid-first/invalid-second creates no release; duplicate channel/base slot creates none; valid track plus blank track creates none; valid resource update plus missing second leaves first unchanged.

- [ ] **Step 2: Run and observe partial behavior**

~~~bash
go test ./internal/service -run 'Test(CreateRelease|CreateTracks|UpdateResourceRevisions).*(Empty|Mixed|Duplicate)' -count=1
~~~

Expected: empty release publishes package and mixed batches can partially write.

- [ ] **Step 3: Split release validation from writes**

Use exact signatures:

~~~go
func (s *Service) normalizeRelease(ctx context.Context, repository repo.PackageRepo, identity core.Identity, packageID string, request core.Release, now time.Time) (core.Release, error)
func (s *Service) validateReleaseResources(ctx context.Context, repository repo.PackageRepo, packageID string, packageRevision int, resources []core.ReleaseResourceRef) error
~~~

Reject empty input. Under one lock, normalize every release, enforce channel scope, verify revision/resources/compatibility, reject duplicate resource names, and detect duplicate slot by channel plus JSON-marshaled validated base (`null` for nil). Only then replace releases and update package once. Log after commit.

- [ ] **Step 4: Validate tracks before transaction writes**

Run every request through `core.NewTrack`, reject duplicate request names, then call `CreateTracks` once inside the package lock. Existing rows remain ignored and returned count remains insert count.

- [ ] **Step 5: Test and commit**

~~~bash
go test ./internal/service ./internal/api -run 'Test(CreateRelease|CreateTracks|UpdateResourceRevisions|Release)' -count=1
git add internal/service/releases.go internal/service/resources.go internal/service/service_test.go internal/api/http_test.go
git commit -m "fix: make publication batches atomic"
~~~

Expected: invalid/empty/mixed batches write nothing.

### Task 9: Serialized unregister and reference-safe purge

**Files:**
- Modify: `internal/service/packages.go`
- Test: `internal/service/service_test.go`
- Modify: `internal/repo/interface.go`
- Modify: `internal/repo/queries/revisions.sql`
- Modify: `internal/repo/postgres_revisions.go`
- Modify: `internal/repo/sqlite.go`
- Modify: `internal/repo/memory.go`
- Test: `internal/repo/memory_test.go`
- Regenerate: `internal/repo/db/revisions.sql.go`, `querier.go`
- Test: `internal/cleanup/worker_test.go`

**Interfaces:**
- Consumes: package lock and cleanup jobs/reference checks.
- Produces: valid unregister/publication race outcomes and durable purge.

- [ ] **Step 1: Add failing race/sharing/retry tests**

Use SQLite to race unregister against push and accept only `(push success, unregister invalid)` or `(unregister success, push package-not-found)`. Create two historical revisions sharing one key; purge one, run cleanup once, and assert blob remains. With failing blob delete, assert metadata gone and job retryable; after recovery assert deletion. Assert upload history remains and consumed package becomes empty. Add a direct Memory transaction case that deletes a package, enqueues cleanup, then returns `assert.AnError`; assert the package remains and no job is claimable.

- [ ] **Step 2: Run and verify defects**

~~~bash
go test ./internal/service ./internal/cleanup -run 'Test(UnregisterRacesPublication|PurgePackage.*(Shared|Retry|History|Jobs))' -count=1
~~~

Expected: current race and immediate best-effort deletion violate tests.

- [ ] **Step 3: Lock unregister check plus delete**

Authorize outside. Inside `withLockedPackageTransaction`, list revisions through the transaction repository, reject when nonempty, then delete locked package. If `LockPackage` reports `repo.ErrNotFound` after the preliminary lookup, translate it to the existing service `package-not-found` response in unregister, revision publication, and resource publication rather than returning a wrapped repository error. Log/audit after commit.

- [ ] **Step 4: Delete purge metadata and enqueue jobs atomically**

Inside the lock: collect unique revision/resource keys and saved package name/project; delete package; for each candidate call `BlobObjectKeyReferenced` against remaining rows and enqueue only unreferenced keys; enqueue one OCI-project job when project is nonempty. Do not delete upload history or call external stores from service.

After the purge caller moves, remove `DeleteUploadsByObjectKeys` from `PackageRepo`, all three backends, `internal/repo/queries/revisions.sql`, generated sqlc, and the memory error-path test table. Run `rg -n 'DeleteUploadsByObjectKeys' internal --glob '*.go' --glob '*.sql'` and require no non-generated caller before committing.

- [ ] **Step 5: Mirror cascades in memory**

Under `mu`, delete package revisions/releases/definitions/resource revisions/ACL and clear `ConsumedByPackageID` on matching uploads; retain uploads. This makes reference queries match SQL backends.

- [ ] **Step 6: Test/race and commit**

~~~bash
go test ./internal/service ./internal/repo ./internal/cleanup -run 'Test(Unregister|Purge|MemoryDeletePackage)' -count=1
go test -race ./internal/service -run 'TestUnregisterRacesPublication' -count=1
git add internal/service/packages.go internal/service/service_test.go internal/repo/interface.go internal/repo/queries/revisions.sql internal/repo/postgres_revisions.go internal/repo/sqlite.go internal/repo/memory.go internal/repo/memory_test.go internal/repo/db/revisions.sql.go internal/repo/db/querier.go internal/cleanup/worker_test.go
git commit -m "fix: serialize package deletion and artifact cleanup"
~~~

Expected: one valid race outcome; shared artifacts survive.

### Task 10: Exactly-one-binary multipart behavior

**Files:**
- Modify: `internal/api/http_revisions.go`
- Test: `internal/api/http_test.go`
- Modify: `internal/service/revisions.go`

**Interfaces:**
- Consumes: `StageUploadStream`, `FinalizeUpload`, and `AbandonUpload`.
- Produces: one accepted binary or rejected/non-consumable first upload.

- [ ] **Step 1: Add failing multipart tests**

Add a handler helper returning repository/storage. Test two binary parts returns 400, creates only one row, rejects it, queues cleanup; truncated multipart after one binary rejects it; nonbinary parts plus one binary finalize successfully as pending. Before EOF/finalization the first row must remain `uploading` and therefore non-consumable.

- [ ] **Step 2: Run and observe last-ID behavior**

~~~bash
go test ./internal/api -run 'TestUnscannedUpload(RejectsSecond|ParseFailure|IgnoresNonBinary)' -count=1
~~~

Expected: two binaries currently succeed with last ID and late parse failure leaves first pending.

- [ ] **Step 3: Enforce one binary**

Track `*core.Upload` returned by `StageUploadStream`. On `NextPart` error after staging, call `AbandonUpload` and fail. On second binary, drain/close without staging it, abandon the first, and return 400. On EOF with exactly one staged upload, call `FinalizeUpload`; return its ID only after activation succeeds. Preserve body-limit 413 mapping. If abandonment fails, log it and return 500; the failed transition leaves the row `uploading`, which is still non-consumable.

- [ ] **Step 4: Test and commit**

~~~bash
go test ./internal/api ./internal/service -run 'TestUnscannedUpload|TestAbandonUpload' -count=1
git add internal/api/http_revisions.go internal/api/http_test.go internal/service/revisions.go internal/service/service_test.go
git commit -m "fix: reject multipart uploads with multiple binaries"
~~~

Expected: no failed multipart request leaves a reported consumable upload.

### Task 11: Slice-wide verification and sync handoff

**Files:**
- Modify only files already listed if verification exposes a defect.

**Interfaces:**
- Produces for slice 3 exactly: `PackageRepo.LockPackage`, embedded `CleanupRepo`, the three cleanup constructors, and `charm.ParseMetadata`.

- [ ] **Step 1: Verify obsolete lifecycle APIs and unsafe keys are gone**

~~~bash
rg -n 'ApproveUpload|DeleteUploadsByObjectKeys|filepath.Join\("uploads"|path.Join\("uploads"' internal --glob '*.go' --glob '*.sql'
~~~

Expected: no matches. Remove now-unused `DeleteStaleUploads` query only if `rg` confirms it has no active non-generated caller.

- [ ] **Step 2: Verify handoff signatures and compile sync**

~~~bash
rg -n 'LockPackage\(|type CleanupRepo|NewOCIImageCleanupJob|func ParseMetadata' internal
go test ./internal/sync -run '^$'
~~~

Expected: all interfaces are present and sync compiles unchanged.

- [ ] **Step 3: Format and verify generation**

~~~bash
make fmt
go tool sqlc generate
make sqlc-diff
git diff --check
~~~

Expected: all commands exit 0 and diff check is silent.

- [ ] **Step 4: Run focused suites**

~~~bash
go test ./internal/core ./internal/charm ./internal/repo ./internal/cleanup ./internal/service ./internal/oci ./internal/api ./internal/app -count=1
~~~

Expected: all PASS.

- [ ] **Step 5: Run broad non-integration verification**

~~~bash
make test-race
make vet
make build
make tidy-check
~~~

Expected: all exit 0 with no race, vet finding, build error, or module diff.

- [ ] **Step 6: Inspect scope and migration order**

~~~bash
git status --short
git diff --stat
git diff -- internal/repo/migrations internal/repo/sqlite/migrations internal/repo/interface.go internal/core/cleanup.go internal/charm/manifest.go
~~~

Expected: changes are confined to slice-2 code/generated SQL; migrations are PostgreSQL 0010-0011 and SQLite 0004-0005; integration tests and unrelated files are untouched.

- [ ] **Step 7: Commit verification-only adjustments if present**

~~~bash
git add internal go.mod go.sum
git commit -m "chore: verify artifact lifecycle hardening"
~~~

Expected: a small verification commit only when tracked corrections were necessary; otherwise the worktree already contains no uncommitted slice files.
