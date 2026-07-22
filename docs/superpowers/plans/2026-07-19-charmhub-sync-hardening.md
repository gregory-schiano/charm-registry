# Charmhub Synchronization Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Charmhub synchronization single-owner per package, transactionally idempotent, safe to retry after partial imports, and unable to prune retained releases or OCI content.

**Architecture:** A backend-specific `repo.SyncLease` surrounds the entire package reconciliation and supplies the transaction boundary used by every synchronized mutation. Network, archive, blob upload, and OCI mirroring stay outside transactions; short fenced transactions lock the package, persist complete revision or resource/release stages, and enqueue destructive cleanup jobs atomically with metadata deletion.

**Tech Stack:** Go 1.26.0 (toolchain 1.26.4), PostgreSQL/pgx v5, SQLite/modernc, sqlc, `context`, `testify`, existing blob and OCI abstractions.

## Global Constraints

- Execute this plan only after the artifact/publication plan is complete; the exact prerequisite interfaces are listed below.
- Existing HTTP JSON response shapes do not change.
- PostgreSQL and SQLite migrations are forward-only; do not add rollback migrations.
- PostgreSQL migrations `0010_upload_lifecycle.sql` and `0011_cleanup_jobs.sql`, and SQLite migrations `0004_upload_lifecycle.sql` and `0005_cleanup_jobs.sql`, belong to the prerequisite slice. This plan's SQLite lease migration is `0006_charmhub_sync_leases.sql`; PostgreSQL advisory locks require no schema migration.
- Keep downloads, archive parsing, blob writes, and OCI calls outside database transactions.
- Every package mutation transaction calls `PackageRepo.LockPackage` before reading or writing mutable package state.
- Every destructive external effect is represented by a cleanup job committed in the same transaction as metadata deletion; do not call `blob.Store.Delete`, `OCIRegistry.DeleteImage`, or `OCIRegistry.DeletePackage` from pruning or package cleanup.
- Regenerate `internal/repo/db/*.sql.go` only with `go tool sqlc generate` after reviewing handwritten SQL.
- Start each behavior with a focused failing unit test. Use SQLite-backed unit tests for rollback semantics; no live PostgreSQL or integration environment is required.
- Preserve unrelated working-tree changes and avoid unrelated file moves.

---

## Prerequisite Interfaces from the Artifact/Publication Plan

This plan is independently executable once `docs/superpowers/plans/2026-07-19-artifact-lifecycle-hardening.md` (`Artifact Lifecycle Hardening Implementation Plan`) has provided these exact contracts:

```go
// internal/repo/interface.go
type PackageRepo interface {
	LockPackage(ctx context.Context, packageID string) (core.Package, error)
}

type CleanupRepo interface {
	EnqueueCleanupJob(ctx context.Context, job core.CleanupJob) error
	ClaimCleanupJobs(ctx context.Context, claim CleanupClaim) ([]core.CleanupJob, error)
	CompleteCleanupJob(ctx context.Context, jobID, workerID string, completedAt time.Time) error
	FailCleanupJob(ctx context.Context, jobID, workerID, lastError string, nextAttemptAt time.Time) error
	BlobObjectKeyReferenced(ctx context.Context, objectKey string) (bool, error)
	OCIImageDigestReferenced(ctx context.Context, packageID, resourceName, digest string) (bool, error)
}

type CleanupClaim struct {
	WorkerID   string
	Now        time.Time
	ClaimUntil time.Time
	Limit      int
}
```

```go
// internal/core/cleanup.go
type CleanupJobKind string

const (
	CleanupJobBlobObject       CleanupJobKind = "blob-object"
	CleanupJobOCIImageManifest CleanupJobKind = "oci-image-manifest"
	CleanupJobOCIProject       CleanupJobKind = "oci-project"
)

type CleanupTarget struct {
	ObjectKey    string
	PackageID    string
	PackageName  string
	OCIProject   string
	ResourceName string
	Digest       string
}

type CleanupJob struct {
	ID             string
	Kind           CleanupJobKind
	DedupeKey      string
	Target         CleanupTarget
	Attempts       int
	NextAttemptAt  time.Time
	LastError      string
	ClaimedBy      string
	ClaimExpiresAt *time.Time
	CompletedAt    *time.Time
	CreatedAt      time.Time
}

func NewBlobCleanupJob(id, objectKey string, now time.Time) (CleanupJob, error)
func NewOCIImageCleanupJob(id, packageID, packageName, ociProject, resourceName, digest string, now time.Time) (CleanupJob, error)
func NewOCIProjectCleanupJob(id, packageName, ociProject string, now time.Time) (CleanupJob, error)
```

`repo.CompositeRepo` embeds `CleanupRepo`. Cleanup enqueue is idempotent by the constructors' canonical `DedupeKey` and the repository's active-target uniqueness rule.

```go
// internal/charm/manifest.go
func ParseMetadata(metadataYAML string) (core.CharmManifest, error)
```

`ParseMetadata` includes container-derived resource declarations. Do not duplicate YAML parsing in the sync package.

## File Map

- Create `internal/repo/sqlite/migrations/0006_charmhub_sync_leases.sql`: durable SQLite holder, fence, and expiry row.
- Create `internal/repo/sync_lease.go`: shared lease errors and backend-neutral lease interface.
- Create `internal/repo/memory_sync_lease.go`: keyed in-process lease implementation.
- Create `internal/repo/sqlite_sync_lease.go`: expiring, renewing, fenced SQLite lease.
- Create `internal/repo/postgres_sync_lease.go`: dedicated-session PostgreSQL advisory lease.
- Create `internal/repo/sync_lease_test.go`: backend-focused lease contract tests and PostgreSQL connection fakes.
- Modify `internal/repo/interface.go`: conditional rule update and `SyncLeaseRepo` contracts.
- Modify `internal/repo/queries/charmhub_sync.sql`: compare-and-set rule status query.
- Modify `internal/repo/queries/releases.sql`: JSONB equality and escaped track matching.
- Modify `internal/repo/postgres_charmhub_sync.go`, `internal/repo/postgres_releases.go`, `internal/repo/sqlite.go`, and `internal/repo/memory.go`: implement the corrected repository behavior.
- Regenerate `internal/repo/db/charmhub_sync.sql.go`, `internal/repo/db/releases.sql.go`, and `internal/repo/db/querier.go`.
- Create `internal/sync/lease.go`: acquisition, neutral-busy outcome, release, and conditional status helpers.
- Create `internal/sync/import_stages.go`: revision repair/import and resource-plus-release persistence stages.
- Create `internal/sync/prune.go`: atomic reference analysis, metadata deletion, and cleanup enqueue.
- Create `internal/sync/lease_test.go`, `internal/sync/import_stages_test.go`, and `internal/sync/prune_test.go`: focused behavioral tests.
- Modify `internal/sync/service.go`: wire leases and make manager backoff lease-aware.
- Modify `internal/sync/reconcile.go`: delegate persistence and pruning to the focused components.
- Modify `internal/sync/oci.go`: prepare OCI package metadata without persisting it outside the resource/release transaction.
- Modify `internal/sync/service_test.go`: retain the existing fixture helpers and update assertions for valid retryable intermediate revisions and durable cleanup.

---

### Task 1: Conditional Rule Updates and Safe Stale-Release SQL

**Files:**
- Modify: `internal/repo/interface.go`
- Modify: `internal/repo/queries/charmhub_sync.sql`
- Modify: `internal/repo/queries/releases.sql`
- Modify: `internal/repo/postgres_charmhub_sync.go`
- Modify: `internal/repo/postgres_releases.go`
- Modify: `internal/repo/sqlite.go`
- Modify: `internal/repo/memory.go`
- Modify: `internal/repo/memory_test.go`
- Modify: `internal/repo/sqlite_test.go`
- Create: `internal/repo/postgres_releases_test.go`
- Regenerate: `internal/repo/db/charmhub_sync.sql.go`
- Regenerate: `internal/repo/db/releases.sql.go`
- Regenerate: `internal/repo/db/querier.go`

**Interfaces:**
- Consumes: existing `repo.ReleaseVariant` and `escapeLikePattern(string) string`.
- Produces: `UpdateCharmhubSyncRuleIfStatus(ctx context.Context, rule core.CharmhubSyncRule, expectedStatus string) (bool, error)` on `CharmhubSyncRepo`.
- Produces: `DeleteStaleTrackReleases` whose `track` argument is always treated literally and whose keep variants compare `base` as JSON rather than serialized JSON text.

- [ ] **Step 1: Add failing compare-and-set tests for memory and SQLite**

Add this test to both `internal/repo/memory_test.go` and `internal/repo/sqlite_test.go`, using `repository := NewMemory()` in the memory file and `repository := newSQLiteTestRepository(t)` plus a persisted account in the SQLite file:

```go
func testConditionalCharmhubRuleUpdate(t *testing.T, repository Backend, accountID string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	rule := core.CharmhubSyncRule{
		PackageName:        "demo",
		Track:              "latest",
		CreatedByAccountID: accountID,
		CreatedAt:          now,
		UpdatedAt:          now,
		LastSyncStatus:     "pending",
	}
	require.NoError(t, repository.CreateCharmhubSyncRule(ctx, rule))

	running := rule
	running.LastSyncStatus = "running"
	running.UpdatedAt = now.Add(time.Second)
	updated, err := repository.UpdateCharmhubSyncRuleIfStatus(ctx, running, "pending")
	require.NoError(t, err)
	assert.True(t, updated)

	staleCompletion := running
	staleCompletion.LastSyncStatus = "ok"
	updated, err = repository.UpdateCharmhubSyncRuleIfStatus(ctx, staleCompletion, "pending")
	require.NoError(t, err)
	assert.False(t, updated)

	rules, err := repository.ListCharmhubSyncRulesByPackageName(ctx, "demo")
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, "running", rules[0].LastSyncStatus)
}
```

The concrete wrappers are:

```go
func TestMemoryConditionalCharmhubRuleUpdate(t *testing.T) {
	t.Parallel()
	testConditionalCharmhubRuleUpdate(t, NewMemory(), "account-1")
}
```

```go
func TestSQLiteConditionalCharmhubRuleUpdate(t *testing.T) {
	t.Parallel()
	repository := newSQLiteTestRepository(t)
	account := ensureSQLiteAccount(t, repository, "account-1", "owner")
	testConditionalCharmhubRuleUpdate(t, repository, account.ID)
}
```

- [ ] **Step 2: Run the conditional-update tests and verify the missing method failure**

Run:

```bash
go test ./internal/repo -run 'Test(Memory|SQLite)ConditionalCharmhubRuleUpdate' -count=1
```

Expected: build failure stating that `UpdateCharmhubSyncRuleIfStatus` is undefined or missing from `Backend`.

- [ ] **Step 3: Add the conditional repository contract and implement it in memory and SQLite**

Add this method beside the existing `UpdateCharmhubSyncRule` in `CharmhubSyncRepo`; retain the old method until Task 2 has migrated every sync caller so Task 1 remains repository-wide buildable:

```go
UpdateCharmhubSyncRuleIfStatus(
	ctx context.Context,
	rule core.CharmhubSyncRule,
	expectedStatus string,
) (bool, error)
```

Add the memory method:

```go
func (m *Memory) UpdateCharmhubSyncRuleIfStatus(
	_ context.Context,
	rule core.CharmhubSyncRule,
	expectedStatus string,
) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	byTrack, ok := m.syncRules[rule.PackageName]
	if !ok {
		return false, nil
	}
	current, ok := byTrack[rule.Track]
	if !ok || current.LastSyncStatus != expectedStatus {
		return false, nil
	}
	byTrack[rule.Track] = rule
	return true, nil
}
```

Add the SQLite method:

```go
func (s *SQLite) UpdateCharmhubSyncRuleIfStatus(
	ctx context.Context,
	rule core.CharmhubSyncRule,
	expectedStatus string,
) (bool, error) {
	result, err := s.db.ExecContext(ctx, `
UPDATE charmhub_sync_rules
SET updated_at = ?, last_sync_status = ?, last_sync_started_at = ?,
    last_sync_finished_at = ?, last_sync_error = ?
WHERE package_name = ? AND track = ? AND last_sync_status = ?`,
		rule.UpdatedAt, rule.LastSyncStatus, rule.LastSyncStartedAt,
		rule.LastSyncFinishedAt, rule.LastSyncError,
		rule.PackageName, rule.Track, expectedStatus)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}
```

- [ ] **Step 4: Add the PostgreSQL compare-and-set query and adapter**

Add this query after the existing `UpdateCharmhubSyncRule` query:

```sql
-- name: UpdateCharmhubSyncRuleIfStatus :execrows
UPDATE charmhub_sync_rules
SET updated_at = sqlc.arg(updated_at),
    last_sync_status = sqlc.arg(last_sync_status),
    last_sync_started_at = sqlc.arg(last_sync_started_at),
    last_sync_finished_at = sqlc.arg(last_sync_finished_at),
    last_sync_error = sqlc.arg(last_sync_error)
WHERE package_name = sqlc.arg(package_name)
  AND track = sqlc.arg(track)
  AND last_sync_status = sqlc.arg(expected_status);
```

Regenerate once, then add the PostgreSQL adapter:

```go
func (p *Postgres) UpdateCharmhubSyncRuleIfStatus(
	ctx context.Context,
	rule core.CharmhubSyncRule,
	expectedStatus string,
) (bool, error) {
	rows, err := p.queries().UpdateCharmhubSyncRuleIfStatus(ctx, sqlcdb.UpdateCharmhubSyncRuleIfStatusParams{
		PackageName:        rule.PackageName,
		Track:              rule.Track,
		UpdatedAt:          rule.UpdatedAt,
		LastSyncStatus:     rule.LastSyncStatus,
		LastSyncStartedAt:  timestamptzPtr(rule.LastSyncStartedAt),
		LastSyncFinishedAt: timestamptzPtr(rule.LastSyncFinishedAt),
		LastSyncError:      rule.LastSyncError,
		ExpectedStatus:     expectedStatus,
	})
	return rows == 1, err
}
```

- [ ] **Step 5: Add failing PostgreSQL payload/LIKE and SQLite wildcard tests**

In `internal/repo/postgres_releases_test.go`, add a `postgresDB` capture double and assert both argument shape and generated SQL:

```go
type captureReleaseDB struct {
	query string
	args  []any
}

func (d *captureReleaseDB) Exec(_ context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	d.query = query
	d.args = append([]any(nil), args...)
	return pgconn.NewCommandTag("DELETE 0"), nil
}

func (d *captureReleaseDB) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	return nil, errors.New("unexpected Query")
}

func (d *captureReleaseDB) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	return nil
}

func TestPostgresDeleteStaleTrackReleasesUsesJSONBaseAndEscapedTrack(t *testing.T) {
	t.Parallel()
	database := &captureReleaseDB{}
	repository := &Postgres{db: database}
	base := &core.Base{Name: "ubuntu", Channel: "24.04", Architecture: "amd64"}

	_, err := repository.DeleteStaleTrackReleases(context.Background(), "pkg-1", `edge%_\\`, []ReleaseVariant{{
		Channel: "edge%_\\/stable",
		Base:    base,
	}})
	require.NoError(t, err)
	require.Len(t, database.args, 3)
	assert.Equal(t, `edge\%\_\\\\/%`, database.args[1])
	assert.Contains(t, database.query, "item->'base'")
	assert.Contains(t, database.query, "ESCAPE")

	payload, ok := database.args[2].(json.RawMessage)
	require.True(t, ok)
	var variants []struct {
		Channel string     `json:"channel"`
		Base    *core.Base `json:"base"`
	}
	require.NoError(t, json.Unmarshal(payload, &variants))
	require.Len(t, variants, 1)
	assert.Equal(t, base, variants[0].Base)
}
```

In `internal/repo/sqlite_test.go`, add a table-driven test that seeds similarly named tracks and prunes only the literal one:

```go
func TestSQLiteDeleteStaleTrackReleasesEscapesWildcards(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := newSQLiteTestRepository(t)
	owner := ensureSQLiteAccount(t, repository, "owner-wildcard", "owner-wildcard")
	pkg := createRepositoryBehaviorTestPackage(t, repository, owner, core.Package{ID: "pkg-wildcard", Name: "wildcard"})
	now := time.Now().UTC()
	for index, channel := range []string{"100%/stable", "100x/stable", "under_score/stable", "underXscore/stable", `back\\slash/stable`} {
		require.NoError(t, repository.ReplaceRelease(ctx, pkg.ID, core.Release{
			ID: "release-" + strconv.Itoa(index), Channel: channel, Revision: index + 1, When: now,
		}))
	}

	for _, track := range []string{"100%", "under_score", `back\\slash`} {
		deleted, err := repository.DeleteStaleTrackReleases(ctx, pkg.ID, track, nil)
		require.NoError(t, err)
		assert.Equal(t, int64(1), deleted)
	}

	releases, err := repository.ListReleases(ctx, pkg.ID)
	require.NoError(t, err)
	channels := make([]string, 0, len(releases))
	for _, release := range releases {
		channels = append(channels, release.Channel)
	}
	assert.ElementsMatch(t, []string{"100x/stable", "underXscore/stable"}, channels)
}
```

- [ ] **Step 6: Run the new pruning tests and verify they fail on text JSON and wildcard matching**

Run:

```bash
go test ./internal/repo -run 'Test(PostgresDeleteStaleTrackReleasesUsesJSONBaseAndEscapedTrack|SQLiteDeleteStaleTrackReleasesEscapesWildcards)' -count=1
```

Expected: PostgreSQL assertion failure because the payload contains `base_key` as a string and the query uses `item->>'base_key'`; SQLite deletes more than one wildcard-matched track.

- [ ] **Step 7: Compare PostgreSQL bases as JSONB and escape track prefixes in both backends**

Replace the stale-release CTE and predicate with:

```sql
-- name: DeleteStaleTrackReleases :execrows
WITH keep_variants AS (
    SELECT item->>'channel' AS channel, item->'base' AS base
    FROM jsonb_array_elements(sqlc.arg(keep_variants)::jsonb) AS item
)
DELETE FROM releases
WHERE package_id = sqlc.arg(package_id)
  AND channel LIKE sqlc.arg(track_prefix) ESCAPE '\'
  AND NOT EXISTS (
      SELECT 1
      FROM keep_variants keep
      WHERE keep.channel = releases.channel
        AND keep.base = releases.base
  );
```

Use this payload type and prefix in `internal/repo/postgres_releases.go`:

```go
type releaseVariantPayload struct {
	Channel string     `json:"channel"`
	Base    *core.Base `json:"base"`
}

payload := make([]releaseVariantPayload, 0, len(keep))
for _, variant := range keep {
	payload = append(payload, releaseVariantPayload{Channel: variant.Channel, Base: variant.Base})
}
keepJSON, err := rawJSON(payload)
if err != nil {
	return 0, err
}
return p.queries().DeleteStaleTrackReleases(ctx, sqlcdb.DeleteStaleTrackReleasesParams{
	PackageID:    packageID,
	TrackPrefix:  escapeLikePattern(track) + "/%",
	KeepVariants: keepJSON,
})
```

In SQLite, change the first arguments and predicate to:

```go
args := []any{packageID, escapeLikePattern(track) + "/%"}
query := `DELETE FROM releases WHERE package_id = ? AND channel LIKE ? ESCAPE '\'`
```

- [ ] **Step 8: Regenerate SQL, format, and run focused repository verification**

Run:

```bash
go tool sqlc generate
make fmt
go test ./internal/repo -run 'Test(Memory|SQLite)ConditionalCharmhubRuleUpdate|Test(PostgresDeleteStaleTrackReleasesUsesJSONBaseAndEscapedTrack|SQLiteDeleteStaleTrackReleasesEscapesWildcards)' -count=1
make sqlc-diff
```

Expected: all selected tests pass and `make sqlc-diff` exits zero with no generated diff.

- [ ] **Step 9: Commit the repository correctness slice**

```bash
git add internal/repo/interface.go internal/repo/queries/charmhub_sync.sql internal/repo/queries/releases.sql internal/repo/postgres_charmhub_sync.go internal/repo/postgres_releases.go internal/repo/sqlite.go internal/repo/memory.go internal/repo/memory_test.go internal/repo/sqlite_test.go internal/repo/postgres_releases_test.go internal/repo/db/charmhub_sync.sql.go internal/repo/db/releases.sql.go internal/repo/db/querier.go
git commit -m "fix(repo): make sync updates and pruning conditional"
```

---

### Task 2: Per-Package Sync Leases and Neutral Manager Skips

**Files:**
- Create: `internal/repo/sqlite/migrations/0006_charmhub_sync_leases.sql`
- Create: `internal/repo/sync_lease.go`
- Create: `internal/repo/memory_sync_lease.go`
- Create: `internal/repo/sqlite_sync_lease.go`
- Create: `internal/repo/postgres_sync_lease.go`
- Create: `internal/repo/sync_lease_test.go`
- Modify: `internal/repo/interface.go`
- Modify: `internal/repo/memory.go`
- Modify: `internal/repo/memory_test.go`
- Modify: `internal/repo/sqlite.go`
- Modify: `internal/repo/postgres.go`
- Modify: `internal/repo/postgres_charmhub_sync.go`
- Modify: `internal/repo/postgres_test.go`
- Modify: `internal/repo/queries/charmhub_sync.sql`
- Regenerate: `internal/repo/db/charmhub_sync.sql.go`, `querier.go`
- Create: `internal/sync/lease.go`
- Create: `internal/sync/lease_test.go`
- Modify: `internal/sync/service.go`
- Modify: `internal/sync/reconcile.go`

**Interfaces:**
- Consumes: `repo.CompositeRepo`, `repo.Transactor`, and the conditional rule update from Task 1.
- Produces:

```go
var ErrSyncLeaseLost = errors.New("sync lease lost")

type SyncLease interface {
	Context() context.Context
	FencingToken() int64
	WithinTransaction(ctx context.Context, fn func(CompositeRepo) error) error
	Release(ctx context.Context) error
}

type SyncLeaseRepo interface {
	TryAcquireSyncLease(
		ctx context.Context,
		packageName string,
		holderID string,
		ttl time.Duration,
	) (lease SyncLease, acquired bool, err error)
}
```

`Backend` embeds `SyncLeaseRepo`; `CompositeRepo` does not. PostgreSQL and memory return fencing token `0`; SQLite returns its monotonically increasing token.

- [ ] **Step 1: Add failing backend lease contract tests**

Create `internal/repo/sync_lease_test.go` with these three invariants:

```go
func exerciseSyncLeaseExclusion(t *testing.T, repository Backend) {
	t.Helper()
	ctx := context.Background()
	first, acquired, err := repository.TryAcquireSyncLease(ctx, "demo", "worker-1", time.Minute)
	require.NoError(t, err)
	require.True(t, acquired)
	t.Cleanup(func() { _ = first.Release(context.Background()) })

	second, acquired, err := repository.TryAcquireSyncLease(ctx, "demo", "worker-2", time.Minute)
	require.NoError(t, err)
	assert.False(t, acquired)
	assert.Nil(t, second)

	other, acquired, err := repository.TryAcquireSyncLease(ctx, "other", "worker-2", time.Minute)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NoError(t, other.Release(ctx))
}

func TestMemorySyncLeaseExcludesSamePackage(t *testing.T) {
	t.Parallel()
	exerciseSyncLeaseExclusion(t, NewMemory())
}

func TestSQLiteSyncLeaseExcludesAcrossRepositoryInstancesAndFences(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "registry.sqlite")
	firstRepo, err := NewSQLite(ctx, path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = firstRepo.Close() })
	require.NoError(t, firstRepo.Migrate(ctx))
	secondRepo, err := NewSQLite(ctx, path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = secondRepo.Close() })
	require.NoError(t, secondRepo.Migrate(ctx))

	first, acquired, err := firstRepo.TryAcquireSyncLease(ctx, "demo", "worker-1", time.Second)
	require.NoError(t, err)
	require.True(t, acquired)
	firstFence := first.FencingToken()
	_, acquired, err = secondRepo.TryAcquireSyncLease(ctx, "demo", "worker-2", time.Second)
	require.NoError(t, err)
	assert.False(t, acquired)
	require.NoError(t, first.Release(ctx))

	second, acquired, err := secondRepo.TryAcquireSyncLease(ctx, "demo", "worker-2", time.Second)
	require.NoError(t, err)
	require.True(t, acquired)
	assert.Greater(t, second.FencingToken(), firstFence)
	require.NoError(t, second.Release(ctx))
}
```

Also add `TestSQLiteSyncLeaseLossCancelsContextAndRejectsMutation`: acquire with a 90 ms TTL, overwrite the row's holder through the second repository, and use `require.Eventually` (one-second deadline, 10 ms poll) to assert `errors.Is(context.Cause(lease.Context()), ErrSyncLeaseLost)`. Then call `lease.WithinTransaction` and assert `errors.Is(err, ErrSyncLeaseLost)`; its callback must not run.

Add a PostgreSQL fake-connection test named `TestPostgresSyncLeaseUnlocksOnDedicatedConnectionAndDiscardsUncertainSession`. Its fake pool returns one fake connection; the fake row sequence returns `true` for `pg_try_advisory_lock` and `false` for `pg_advisory_unlock`. Assert two queries hit the same connection, `Release` was not called, and `Discard` was called once. This is a unit test of connection ownership; do not require a live PostgreSQL server.

Add `TestPostgresSyncLeaseRollbackUsesDetachedContextAfterCancellation`: the fake transaction callback cancels the acquisition context and returns `assert.AnError`; its `Rollback` records `ctx.Err() == nil` inside the method. Assert rollback ran once with a live context and the callback error is preserved. Do not inspect a retained context after return because the helper must cancel its cleanup timeout. This covers the lease-specific transaction path independently of the generic transactor fixed in slice 4.

- [ ] **Step 2: Run the lease tests and verify the missing contract failure**

Run:

```bash
go test ./internal/repo -run 'Test(Memory|SQLite|Postgres)SyncLease' -count=1
```

Expected: build failure because `TryAcquireSyncLease`, `SyncLease`, and `FencingToken` do not exist.

- [ ] **Step 3: Add the shared interface, error, and SQLite schema**

Put the interfaces above in `internal/repo/sync_lease.go`, embed `SyncLeaseRepo` in `Backend`, and create this migration:

```sql
CREATE TABLE IF NOT EXISTS charmhub_sync_leases (
    package_name TEXT PRIMARY KEY,
    holder_id TEXT NOT NULL,
    fencing_token INTEGER NOT NULL,
    expires_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS charmhub_sync_leases_expires_at_idx
    ON charmhub_sync_leases (expires_at);
```

Reject empty package names, empty holder IDs, and non-positive TTLs with descriptive errors in every backend.

- [ ] **Step 4: Implement the memory lease**

Add `syncLeaseMu sync.Mutex`, `syncLeaseHolders map[string]string`, and `syncLeaseFences map[string]int64` to `Memory`, initialize both maps, and implement nonblocking acquisition under `syncLeaseMu`. `memorySyncLease.Release` removes the entry only when its holder still matches. `memorySyncLease.WithinTransaction` delegates to `Memory.WithinTransaction`, and `Context` is canceled on release.

The acquisition's critical section is exactly:

```go
m.syncLeaseMu.Lock()
defer m.syncLeaseMu.Unlock()
if _, held := m.syncLeaseHolders[packageName]; held {
	return nil, false, nil
}
m.syncLeaseHolders[packageName] = holderID
m.syncLeaseFences[packageName]++
leaseCtx, cancel := context.WithCancelCause(ctx)
return &memorySyncLease{
	repository: m,
	packageName: packageName,
	holderID: holderID,
	ctx: leaseCtx,
	cancel: cancel,
}, true, nil
```

- [ ] **Step 5: Implement the renewing SQLite lease**

Use one atomic upsert so only expired rows can be taken over and every takeover increments the fence:

```sql
INSERT INTO charmhub_sync_leases (package_name, holder_id, fencing_token, expires_at)
VALUES (?, ?, 1, ?)
ON CONFLICT(package_name) DO UPDATE SET
    holder_id = excluded.holder_id,
    fencing_token = charmhub_sync_leases.fencing_token + 1,
    expires_at = excluded.expires_at
WHERE charmhub_sync_leases.expires_at <= ?
RETURNING fencing_token;
```

`sqliteSyncLease` stores package, holder, fence, TTL, a cancel-cause context, and stop/done channels. Renew every `ttl/3` with:

```sql
UPDATE charmhub_sync_leases
SET expires_at = ?
WHERE package_name = ? AND holder_id = ? AND fencing_token = ? AND expires_at > ?;
```

If renewal affects zero rows or returns an error, cancel the lease context with an error wrapping `ErrSyncLeaseLost`. `WithinTransaction` first checks `context.Cause(lease.Context())`, then calls `SQLite.WithinTransaction` and verifies this predicate inside the transaction before invoking the callback:

```sql
SELECT 1
FROM charmhub_sync_leases
WHERE package_name = ? AND holder_id = ? AND fencing_token = ? AND expires_at > ?;
```

`Release` stops renewal and conditionally sets `expires_at` to the current UTC time; it does not delete the row, so the next owner receives a larger fencing token.

- [ ] **Step 6: Implement the dedicated PostgreSQL advisory lease**

Add a private pool adapter so tests can fake acquisition:

```go
type postgresSyncLeasePool interface {
	Acquire(ctx context.Context) (postgresSyncLeaseConn, error)
}

type postgresSyncLeaseConn interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	BeginTx(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error)
	Release()
	Discard(ctx context.Context) error
}
```

Add `syncLeasePool postgresSyncLeasePool` to `Postgres` and initialize a pgxpool adapter in `NewPostgres`. Derive the advisory key deterministically:

```go
func syncLeaseAdvisoryKey(packageName string) int64 {
	sum := sha256.Sum256([]byte(packageName))
	return int64(binary.BigEndian.Uint64(sum[:8]))
}
```

Acquire a dedicated connection and scan `SELECT pg_try_advisory_lock($1)`. Release an unacquired connection immediately. `postgresSyncLease.WithinTransaction` starts and commits the transaction on that same dedicated connection, passing `&Postgres{pool: repository.pool, db: tx}` to the callback. Install a panic-safe rollback immediately after `BeginTx`; use a named return and, in the defer, call `tx.Rollback` with a five-second timeout derived from `context.WithoutCancel(ctx)`. Ignore only `pgx.ErrTxClosed` and join any other rollback error with the return error. A canceled callback context must never prevent rollback.

On release, use a five-second timeout derived from `context.WithoutCancel(ctx)` and scan the boolean from `SELECT pg_advisory_unlock($1)`. Call the adapter's `Release` only for `true, nil`; on `false` or error call `Discard`, which uses `pgxpool.Conn.Hijack().Close(detachedCtx)`. Return the unlock/discard error so uncertain sessions are observable.

- [ ] **Step 7: Run backend lease tests**

Run:

```bash
make fmt
go test ./internal/repo -run 'Test(Memory|SQLite|Postgres)SyncLease' -count=1
```

Expected: all selected tests pass. The SQLite tests demonstrate cross-instance exclusion, monotonic fencing, and cancellation after ownership loss; the PostgreSQL test demonstrates uncertain sessions never return to the pool.

- [ ] **Step 8: Add failing manager exclusion and neutral-backoff tests**

Create `internal/sync/lease_test.go` with a shared memory backend and two sync services. Block the first service in `fakeCharmhubClient.DownloadTo`, call `tryReconcilePackage` on the second, and assert `attempted == false`, `err == nil`, and only one revision download started. Add this direct manager assertion:

```go
func TestManagerLeaseBusyDoesNotChangeBackoff(t *testing.T) {
	t.Parallel()
	env := newSyncTestHarness(t)
	held, acquired, err := env.repo.TryAcquireSyncLease(context.Background(), "demo", "other-worker", time.Minute)
	require.NoError(t, err)
	require.True(t, acquired)
	defer held.Release(context.Background())

	manager := newManager(t)
	manager.service = env.sync
	manager.failCounts["demo"] = 2
	manager.failSkip["demo"] = 3
	manager.runPackage(context.Background(), "demo")
	assert.Equal(t, 2, manager.failCounts["demo"])
	assert.Equal(t, 3, manager.failSkip["demo"])
}
```

- [ ] **Step 9: Run the manager tests and verify the missing outcome path**

Run:

```bash
go test ./internal/sync -run 'Test(TwoServicesCannotReconcileSamePackage|ManagerLeaseBusyDoesNotChangeBackoff)' -count=1
```

Expected: build failure because `tryReconcilePackage` and `runPackage` do not exist.

- [ ] **Step 10: Wrap complete reconciliation in one lease and make status transitions conditional**

In `Service`, add `syncLeases repo.SyncLeaseRepo` and `syncLeaseHolderID string`; initialize both in `New`, using one `uuid.NewString()` per service. Use constants `syncLeaseTTL = 2 * time.Minute` and `syncLeaseReleaseTimeout = 5 * time.Second`.

Create `internal/sync/lease.go` with:

```go
func (s *Service) tryReconcilePackage(ctx context.Context, packageName string) (attempted bool, err error) {
	lease, acquired, err := s.syncLeases.TryAcquireSyncLease(
		ctx, packageName, s.syncLeaseHolderID, syncLeaseTTL,
	)
	if err != nil || !acquired {
		return false, err
	}
	attempted = true
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), syncLeaseReleaseTimeout)
		defer cancel()
		err = errors.Join(err, lease.Release(releaseCtx))
	}()
	err = s.reconcilePackageUnderLease(lease.Context(), packageName, lease)
	return attempted, err
}

func (s *Service) reconcilePackage(ctx context.Context, packageName string) error {
	_, err := s.tryReconcilePackage(ctx, packageName)
	return err
}
```

Add `TestReconcilePanicStillReleasesLease`: make the fake Charmhub download panic, recover in the test, then acquire the same package lease from a second holder. The acquisition must succeed, proving the release defer is installed immediately after acquisition and runs during panic unwinding.

Rename the current reconciliation body to `reconcilePackageUnderLease(ctx, packageName, lease)`. Add `Manager.runPackage`: busy logs a debug neutral skip and changes no backoff state; attempted failures call `recordFailure`; attempted successes call `recordSuccess`. Replace duplicate bodies in `runPending` and `runAll` with `runPackage`.

Replace every reconciliation-owned rule update with `UpdateCharmhubSyncRuleIfStatus`. Starting a rule expects its observed status; completion expects `running`. If the update returns false, do not overwrite a concurrent `deleting` state. Run only those reconciliation-owned transitions through `lease.WithinTransaction` so SQLite verifies fence ownership before mutation. Administrative `RemoveCharmhubSyncRule` has no active lease and deliberately races an in-flight reconciliation: it reads the rule and performs `UpdateCharmhubSyncRuleIfStatus` directly against the observed status, returning not-found on a false result. Migrate the existing memory and repository behavior tests from `UpdateCharmhubSyncRule` to conditional updates with explicit expected statuses. After the last production/test caller is migrated, remove `UpdateCharmhubSyncRule` from `CharmhubSyncRepo`, all three backend implementations, and `internal/repo/queries/charmhub_sync.sql`, then regenerate sqlc output. Require this search to be empty before the task commit:

```bash
rg -n 'UpdateCharmhubSyncRule\(' internal --glob '*.go' --glob '!internal/repo/db/**'
```

- [ ] **Step 11: Run lease, status-race, and race-detector verification**

Add `TestDeletingRuleIsNotOverwrittenByInFlightCompletion` by blocking a resource download, calling `RemoveCharmhubSyncRule`, releasing the download, and asserting the final status remains `deleting`.

Run:

```bash
go tool sqlc generate
make fmt
go test ./internal/repo ./internal/sync -run 'SyncLease|LeaseBusy|CannotReconcile|DeletingRuleIsNotOverwritten' -count=1
go test -race ./internal/sync -run 'TestTwoServicesCannotReconcileSamePackage' -count=1
make sqlc-diff
```

Expected: all selected tests pass and the race detector reports no races.

- [ ] **Step 12: Commit the lease slice**

```bash
git add internal/repo/sqlite/migrations/0006_charmhub_sync_leases.sql internal/repo/sync_lease.go internal/repo/memory_sync_lease.go internal/repo/sqlite_sync_lease.go internal/repo/postgres_sync_lease.go internal/repo/sync_lease_test.go internal/repo/interface.go internal/repo/memory.go internal/repo/memory_test.go internal/repo/sqlite.go internal/repo/postgres.go internal/repo/postgres_charmhub_sync.go internal/repo/postgres_test.go internal/repo/queries/charmhub_sync.sql internal/repo/db/charmhub_sync.sql.go internal/repo/db/querier.go internal/sync/lease.go internal/sync/lease_test.go internal/sync/service.go internal/sync/reconcile.go
git commit -m "feat(sync): coordinate reconciliation with package leases"
```

---

### Task 3: Atomic Revision Stage and Legacy Incomplete-Revision Repair

**Files:**
- Create: `internal/sync/import_stages.go`
- Create: `internal/sync/import_stages_test.go`
- Modify: `internal/sync/reconcile.go`

**Interfaces:**
- Consumes: `SyncLease.WithinTransaction`, `PackageRepo.LockPackage`, and `charm.ParseMetadata(string) (core.CharmManifest, error)`.
- Produces: `ensureCharmhubRevisionStage(ctx, lease, pkg, createdBy, info, revisionNumber, trackCache) (core.Package, error)`.
- Produces pure builder `newCharmhubRevision(pkg core.Package, createdBy string, info charmhubclient.PackageChannel, revisionNumber int, objectKey string, artifact *downloadedArtifact) (core.Revision, error)`; `applyCharmhubPackageMetadata` also remains pure and neither builder writes through a repository.

- [ ] **Step 1: Add failing rollback and legacy-repair tests**

In `internal/sync/import_stages_test.go`, define a `wrappingSyncLease` whose `WithinTransaction` delegates to a SQLite backend and wraps the callback's `repo.CompositeRepo`. Define `failingResourceDefinitionRepo` by embedding `repo.CompositeRepo` and overriding `UpsertResourceDefinition` to return `assert.AnError`.

Add `TestCharmhubRevisionStageRollsBackMetadataAndCompensatesBlob`: seed a synchronized SQLite package and track, prepare revision 7 from `newSyncFixture`, run the stage through the failing wrapper, and assert:

```go
_, revisionErr := env.repo.GetRevisionByNumber(ctx, pkg.ID, 7)
assert.ErrorIs(t, revisionErr, repo.ErrNotFound)
definitions, err := env.repo.ListResourceDefinitions(ctx, pkg.ID)
require.NoError(t, err)
assert.Empty(t, definitions)
stored, err := env.repo.GetPackageByID(ctx, pkg.ID)
require.NoError(t, err)
assert.Equal(t, originalTitle, stored.Title)
_, blobErr := env.sync.blobs.Get(ctx, "charms/"+pkg.ID+"/7.charm")
require.Error(t, blobErr)
```

Add `TestCharmhubSyncRepairsLegacyIncompleteRevision`: seed revision 7 with the fixture archive's `MetadataYAML` but no resource definitions, then run reconciliation. Assert `config` and `app-image` definitions, both resource revisions, and the release exist; also assert the charm revision URL's `downloadToCalls` remains zero, proving repair used stored metadata.

- [ ] **Step 2: Run the stage tests and verify current partial-write behavior**

Run:

```bash
go test ./internal/sync -run 'TestCharmhub(RevisionStageRollsBackMetadataAndCompensatesBlob|SyncRepairsLegacyIncompleteRevision)' -count=1
```

Expected: build failure for `ensureCharmhubRevisionStage`; after temporarily targeting existing reconciliation, the legacy test fails with `resource not declared` because an existing revision returns early.

- [ ] **Step 3: Split revision preparation from persistence**

In `internal/sync/import_stages.go`, move construction out of `createRevisionRecord` and use this stage order:

```go
existing, err := s.repo.GetRevisionByNumber(ctx, pkg.ID, revisionNumber)
if err == nil {
	manifest, parseErr := charm.ParseMetadata(existing.MetadataYAML)
	if parseErr != nil {
		return core.Package{}, parseErr
	}
	return s.repairCharmhubRevision(ctx, lease, pkg, info, existing, manifest)
}
if !errors.Is(err, repo.ErrNotFound) {
	return core.Package{}, err
}

artifact, err := s.downloadAndParseRevision(ctx, info.DefaultRelease.Revision.Download)
if err != nil {
	return core.Package{}, err
}
defer artifact.Close()
objectKey := filepath.ToSlash(filepath.Join("charms", pkg.ID, fmt.Sprintf("%d.charm", revisionNumber)))
if err := s.putRevisionBlob(ctx, objectKey, artifact); err != nil {
	return core.Package{}, err
}
revision, err := s.newCharmhubRevision(pkg, createdBy, info, revisionNumber, objectKey, artifact)
if err != nil {
	return core.Package{}, errors.Join(err, s.blobs.Delete(context.WithoutCancel(ctx), objectKey))
}
updated, persistErr := s.persistCharmhubRevision(
	ctx, lease, pkg, info, revision, artifact.Archive.Manifest,
)
if persistErr != nil {
	return core.Package{}, errors.Join(persistErr, s.blobs.Delete(context.WithoutCancel(ctx), objectKey))
}
return updated, nil
```

The compensation key is deterministic and is deleted only when the transaction failed to adopt it. Parsing and blob upload remain outside the transaction.

- [ ] **Step 4: Persist package, revision, and every definition in one locked transaction**

Implement both new-import and repair paths through one helper:

```go
func (s *Service) persistCharmhubRevision(
	ctx context.Context,
	lease repo.SyncLease,
	pkg core.Package,
	info charmhubclient.PackageChannel,
	revision core.Revision,
	manifest core.CharmManifest,
) (core.Package, error) {
	var updated core.Package
	err := lease.WithinTransaction(ctx, func(tx repo.CompositeRepo) error {
		locked, err := tx.LockPackage(ctx, pkg.ID)
		if err != nil {
			return err
		}
		locked = applyCharmhubPackageMetadata(locked, info.Result, manifest)
		locked.Status = "published"
		locked.UpdatedAt = s.now()
		if err := tx.UpdatePackage(ctx, locked); err != nil {
			return err
		}
		if _, err := tx.GetRevisionByNumber(ctx, locked.ID, revision.Revision); errors.Is(err, repo.ErrNotFound) {
			if err := tx.CreateRevision(ctx, revision); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if err := s.upsertManifestResourceDefinitionsWithRepo(ctx, tx, locked.ID, manifest); err != nil {
			return err
		}
		updated = locked
		return nil
	})
	return updated, err
}
```

`repairCharmhubRevision` calls this helper with the existing row. `upsertManifestResourceDefinitionsWithRepo` is the current loop changed to accept `repo.PackageRepo`; it validates every `core.NewResourceDefinition` before the first upsert so invalid manifests cause no writes even in the memory backend.

- [ ] **Step 5: Route reconciliation through the stage and remove poisoned-row cleanup**

Pass `repo.SyncLease` through `syncCharmhubTrack`, `syncTrackReleases`, and `ensureCharmhubArtifacts`. Replace `ensureCharmhubRevisionArtifacts` with `ensureCharmhubRevisionStage` and remove `cleanupFailedCharmhubRevisionImport` plus `deleteEmptyResourceDefinitions`.

A resource-stage failure now intentionally leaves the completed revision and definitions committed but no release. Update `TestCharmhubSyncRejectsResourceDigestMismatch` and `TestCharmhubSyncMirrorFailureMarksRuleErrorAndRetries` to assert that revision 7 and its definitions remain, while resource revisions and releases remain absent. This is the specified retryable intermediate state.

- [ ] **Step 6: Fence package creation, track creation, and final package metadata updates**

Pass the active lease into `ensureSyncedPackage`, `ensureCharmhubTrack`, and `persistSyncedPackage`.

- For an absent package, enter `lease.WithinTransaction`, recheck `GetPackageByName`, create only if still absent, call `LockPackage` after creation, and create the first track before commit.
- For an existing package, call `LockPackage` before `CreateTracks`; update `trackCache` only after commit.
- In `persistSyncedPackage`, lock and re-fetch the package, copy only synchronization-owned presentation fields (`Authority`, `DefaultTrack`, `Status`, `Title`, `Summary`, `Description`, `Website`, `Links`, `Media`, `TrackGuardrails`, and `UpdatedAt`) from the prepared value, and call `UpdatePackage` inside the same lease transaction. Preserve OCI credentials from the locked row unless Task 4 explicitly applies newly prepared OCI metadata.

This ensures SQLite checks the fence for every package/track write, including first reconciliation and final default-track persistence.

- [ ] **Step 7: Run focused revision-stage verification**

Run:

```bash
make fmt
go test ./internal/sync -run 'TestCharmhub(RevisionStageRollsBackMetadataAndCompensatesBlob|SyncRepairsLegacyIncompleteRevision|SyncRejectsResourceDigestMismatch|SyncMirrorFailureMarksRuleErrorAndRetries)' -count=1
```

Expected: all selected tests pass; the repair test does not download the charm again.

- [ ] **Step 8: Commit the revision stage**

```bash
git add internal/sync/import_stages.go internal/sync/import_stages_test.go internal/sync/reconcile.go internal/sync/service_test.go
git commit -m "fix(sync): make revision imports atomic and repairable"
```

---

### Task 4: Atomic Resource-and-Release Stage with Blob Compensation

**Files:**
- Modify: `internal/sync/import_stages.go`
- Modify: `internal/sync/import_stages_test.go`
- Modify: `internal/sync/reconcile.go`
- Modify: `internal/sync/oci.go`
- Modify: `internal/sync/service_test.go`

**Interfaces:**
- Consumes: revision stage from Task 3 and `SyncLease.WithinTransaction` plus `LockPackage`.
- Produces: `persistCharmhubResourceReleaseStage(ctx, lease, pkg, release, prepared) (core.Package, error)`; it creates every missing resource revision and replaces its release in one transaction.
- Produces: `preparedResourceBatch` containing ordered resource revisions, opened OCI descriptor artifacts, and newly uploaded file object keys for compensation.

- [ ] **Step 1: Add failing atomicity tests**

Add `TestCharmhubResourceReleaseStageRollsBackAllResourcesAndRelease` using the SQLite `wrappingSyncLease`. Override `CreateResourceRevision` to fail for resource name `app-image`. Seed revision 7, both definitions, and an old `latest/stable` release at revision 6. Prepare the fixture's file and OCI resources, invoke the stage, and assert the old release remains at revision 6, neither new resource revision exists, and the prepared file key is absent from blob storage.

Add `TestCharmhubResourceReleaseStageLeavesRevisionRetryable`: run full reconciliation with the same injected failure and assert revision 7 plus definitions exist, but no release at revision 7 is visible.

Add `TestCharmhubFileOnlyResourceStagePreservesExistingOCIMetadata`: seed a package with existing OCI project, robot credentials, and sync timestamp; persist a file-only `preparedResourceBatch` whose `ociMetadataPrepared` is false; assert every OCI field remains byte-for-byte unchanged after commit.

- [ ] **Step 2: Run the atomicity tests and verify partial resource persistence**

Run:

```bash
go test ./internal/sync -run 'TestCharmhubResourceReleaseStage(RollsBackAllResourcesAndRelease|LeavesRevisionRetryable)' -count=1
```

Expected: failure because the current loop persists each resource separately and replaces the release afterward.

- [ ] **Step 3: Make resource preparation return an explicit compensation batch**

Use these focused types:

```go
type preparedResourceBatch struct {
	items              []preparedResourceRevision
	fileObjectKeys     []string
	packageWithOCI     core.Package
	ociMetadataPrepared bool
}

func (b *preparedResourceBatch) close() error {
	var errs []error
	for _, item := range b.items {
		if item.artifact != nil {
			errs = append(errs, item.artifact.Close())
		}
	}
	return errors.Join(errs...)
}
```

Initialize every batch with `packageWithOCI: pkg`. Set `ociMetadataPrepared` only after `syncOCIPackage` returns successfully, and then replace `packageWithOCI` with that returned package. Record a file key only after `Put` succeeds. If any parallel download, digest check, or later OCI mirror fails, synchronously delete every recorded file key with `context.WithoutCancel(ctx)` and join compensation errors. OCI content-addressed mirrors are not synchronously deleted.

- [ ] **Step 4: Stop persisting OCI package metadata during preparation**

Change `internal/sync/oci.go` so `syncOCIPackage` only calls `s.oci.SyncPackage` and returns the resulting package; remove its `s.repo.UpdatePackage` call. Add this helper for the later locked merge:

```go
func applyOCIPackageMetadata(target, prepared core.Package) core.Package {
	target.OCIProject = prepared.OCIProject
	target.OCIPushRobot = prepared.OCIPushRobot
	target.OCIPullRobot = prepared.OCIPullRobot
	target.OCISyncedAt = prepared.OCISyncedAt
	return target
}
```

All registry provisioning and mirroring completes before persistence begins.

- [ ] **Step 5: Commit all resource rows and the release together**

Implement the stage transaction in this exact order:

```go
err := lease.WithinTransaction(ctx, func(tx repo.CompositeRepo) error {
	locked, err := tx.LockPackage(ctx, pkg.ID)
	if err != nil {
		return err
	}
	if prepared.ociMetadataPrepared && !core.PackagesEqualForOCI(locked, prepared.packageWithOCI) {
		locked = applyOCIPackageMetadata(locked, prepared.packageWithOCI)
		locked.UpdatedAt = s.now()
		if err := tx.UpdatePackage(ctx, locked); err != nil {
			return err
		}
	}
	for _, item := range prepared.items {
		if item.revision.ID == "" {
			continue
		}
		_, lookupErr := tx.GetResourceRevision(ctx, item.revision.ResourceID, item.revision.Revision)
		if lookupErr == nil {
			continue
		}
		if !errors.Is(lookupErr, repo.ErrNotFound) {
			return lookupErr
		}
		if err := tx.CreateResourceRevision(ctx, item.revision); err != nil {
			return err
		}
	}
	if err := tx.ReplaceRelease(ctx, locked.ID, release); err != nil {
		return err
	}
	updated = locked
	return nil
})
```

On transaction failure, delete every `prepared.fileObjectKeys` entry synchronously. On success, retain the keys because their resource rows adopted them. Close temporary artifacts on both paths.

- [ ] **Step 6: Route each channel through revision then resource/release stages**

Build and validate `core.Release` before resource preparation. Replace the current independent `CreateResourceRevision` loop and later `ReplaceRelease` call with:

```go
pkg, err = s.ensureCharmhubRevisionStage(ctx, lease, pkg, createdBy, state.info, release.Revision, trackCache)
if err != nil {
	return core.Package{}, err
}
prepared, refs, err := s.prepareCharmhubResourceBatch(ctx, pkg, state.info, release.Revision)
if err != nil {
	return core.Package{}, err
}
defer prepared.close()
release.Resources = refs
pkg, err = s.persistCharmhubResourceReleaseStage(ctx, lease, pkg, release, prepared)
if err != nil {
	return core.Package{}, err
}
```

- [ ] **Step 7: Run resource/release stage and retry verification**

Run:

```bash
make fmt
go test ./internal/sync -run 'TestCharmhub(ResourceReleaseStage|SyncMirrorFailureMarksRuleErrorAndRetries|SyncFailureMarksRuleErrorAndKeepsExistingRelease|ReconcileCharmhubPackageCreatesMirroredArtifacts)' -count=1
```

Expected: all selected tests pass; no release ever references a missing resource row.

- [ ] **Step 8: Commit the resource/release stage**

```bash
git add internal/sync/import_stages.go internal/sync/import_stages_test.go internal/sync/reconcile.go internal/sync/oci.go internal/sync/service_test.go
git commit -m "fix(sync): publish resources and releases atomically"
```

---

### Task 5: Transactional Track and Reference-Aware Artifact Pruning

**Files:**
- Create: `internal/sync/prune.go`
- Create: `internal/sync/prune_test.go`
- Modify: `internal/sync/reconcile.go`
- Modify: `internal/sync/service_test.go`

**Interfaces:**
- Consumes: `core.NewBlobCleanupJob`, `core.NewOCIImageCleanupJob`, `core.NewOCIProjectCleanupJob`, `CleanupRepo.EnqueueCleanupJob`, `SyncLease.WithinTransaction`, and `PackageRepo.LockPackage`.
- Produces: `pruneEmptyTrack(ctx, lease, pkg, track, trackCache) (core.Package, error)` with one transaction and post-commit cache mutation.
- Produces: `pruneSyncedPackage(ctx, lease, pkg, rules, trackCache) error` with reference-aware cleanup enqueue.
- Produces: `cleanupSyncedPackage(ctx, lease, packageName) error` that commits metadata deletion and cleanup jobs atomically.

- [ ] **Step 1: Add failing empty-track rollback/error tests**

In `internal/sync/prune_test.go`, use a SQLite `wrappingSyncLease` whose callback repo returns `assert.AnError` from `DeleteTrack` after release deletion. Seed `wild%_track/stable` and the track row, call `pruneEmptyTrack`, and assert both still exist after the error. Add a second subtest where every release/track is missing and assert not-found is tolerated.

Run:

```bash
go test ./internal/sync -run 'TestPruneEmptyTrack(RollsBackOnDeleteError|ToleratesOnlyNotFound)' -count=1
```

Expected: current code returns nil after discarding the injected error and cannot roll back earlier deletes.

- [ ] **Step 2: Make empty-track deletion one fenced transaction**

Implement:

```go
func (s *Service) pruneEmptyTrack(
	ctx context.Context,
	lease repo.SyncLease,
	pkg core.Package,
	track string,
	trackCache *packageTrackCache,
) (core.Package, error) {
	if pkg.ID == "" {
		return pkg, nil
	}
	err := lease.WithinTransaction(ctx, func(tx repo.CompositeRepo) error {
		if _, err := tx.LockPackage(ctx, pkg.ID); err != nil {
			return err
		}
		for _, risk := range charmhubSyncRisks {
			err := tx.DeleteRelease(ctx, pkg.ID, track+"/"+risk)
			if err != nil && !errors.Is(err, repo.ErrNotFound) {
				return err
			}
		}
		err := tx.DeleteTrack(ctx, pkg.ID, track)
		if err != nil && !errors.Is(err, repo.ErrNotFound) {
			return err
		}
		return nil
	})
	if err != nil {
		return core.Package{}, err
	}
	trackCache.remove(pkg.ID, track)
	return pkg, nil
}
```

Pass `lease` and `trackCache` from `syncCharmhubTrack`. Wrap `DeleteStaleTrackReleases` in a lease transaction that locks the package first, so SQLite validates its fence at this mutation boundary.

- [ ] **Step 3: Add failing retained-digest and cleanup-deduplication tests**

Seed one OCI resource definition with revisions 1 and 2 sharing digest `sha256:shared`, and a retained release referencing revision 2. Call `pruneSyncedPackage`; assert revision 1 is deleted, revision 2 remains, and `ClaimCleanupJobs` returns no OCI job.

Add a case with no retained revision and assert one claimed OCI image job has `ResourceName == "app-image"` and `Digest == "sha256:shared"`, despite two stale rows. Add a second resource definition with the same digest and assert it produces a separate job because the repository differs.

Add a file-resource/revision case and assert metadata is deleted immediately while its `CleanupJobBlobObject` remains claimable; the blob itself remains until the prerequisite cleanup worker executes it.

Run:

```bash
go test ./internal/sync -run 'TestPruneSyncedPackage(RetainsSharedOCIDigest|DeduplicatesPerRepositoryAndDigest|EnqueuesBlobCleanupAtomically)' -count=1
```

Expected: current pruning calls OCI/blob deletion before row deletion, and the retained-shared-digest test records an unsafe OCI deletion.

- [ ] **Step 4: Compute references and delete metadata in one transaction**

Move the directly affected prune functions from `reconcile.go` to `prune.go`. Inside one `lease.WithinTransaction`:

1. Call `LockPackage`.
2. List releases; delete only out-of-scope variants and derive kept charm revisions plus kept resource revision numbers from the remaining releases.
3. For each stale charm revision with a nonempty object key, construct and enqueue `core.NewBlobCleanupJob(uuid.NewString(), revision.ObjectKey, s.now())` before deleting its row; historical empty keys create no job and do not block metadata pruning.
4. For each resource definition, load all revisions once and build retained digests from kept revision numbers before processing stale rows.
5. For each stale file revision with a nonempty object key, enqueue a blob cleanup job before row deletion; skip empty historical keys.
6. For each stale OCI revision whose digest is nonempty, whose package OCI project is nonempty, and whose digest is not retained, enqueue at most one job per `resource definition name + NUL + digest`:

```go
job, err := core.NewOCIImageCleanupJob(
	uuid.NewString(), pkg.ID, pkg.Name, pkg.OCIProject, def.Name, revision.OCIImageDigest, s.now(),
)
if err != nil {
	return err
}
if err := tx.EnqueueCleanupJob(ctx, job); err != nil {
	return err
}
```

7. Delete an empty resource definition only after all its stale rows are removed.
8. Delete dangling tracks, collecting their names without mutating `trackCache` inside the transaction.

Only after commit succeeds, remove collected tracks from the cache. Delete `deleteResourceArtifact`; no pruning path calls external storage or OCI deletion.

- [ ] **Step 5: Add failing whole-package cleanup durability test**

Replace `TestCleanupSyncedPackageBestEffortOnOCIError` with `TestCleanupSyncedPackageCommitsMetadataAndDurableJobs`. Seed a synchronized package with one charm blob, one file resource blob, historical charm/resource rows with empty object keys, and an OCI project. Run cleanup with a lease, then assert the package/revisions/resources/releases/tracks are gone and claimed jobs contain exactly the two nonempty blob keys plus one `CleanupJobOCIProject` target. No empty-target job is created. Configure the OCI fake to fail deletion and assert it was not called by synchronization.

Run:

```bash
go test ./internal/sync -run 'TestCleanupSyncedPackageCommitsMetadataAndDurableJobs' -count=1
```

Expected: failure because current cleanup performs external deletion before piecemeal metadata deletion and creates no durable jobs.

- [ ] **Step 6: Make whole-package cleanup atomic and durable**

Implement `cleanupSyncedPackage(ctx, lease, packageName)` as one fenced transaction. Return nil when the package is absent or not Charmhub-managed. After `LockPackage`, list charm/resource object keys and enqueue blob jobs only for nonempty keys; enqueue one project job when `OCIProject` is nonempty:

```go
for _, objectKey := range append(charmObjectKeys, resourceObjectKeys...) {
	if objectKey == "" {
		continue
	}
	job, err := core.NewBlobCleanupJob(uuid.NewString(), objectKey, s.now())
	if err != nil {
		return err
	}
	if err := tx.EnqueueCleanupJob(ctx, job); err != nil {
		return err
	}
}
if pkg.OCIProject != "" {
	projectJob, err := core.NewOCIProjectCleanupJob(
		uuid.NewString(), pkg.Name, pkg.OCIProject, s.now(),
	)
	if err != nil {
		return err
	}
	if err := tx.EnqueueCleanupJob(ctx, projectJob); err != nil {
		return err
	}
}
```

Delete resource revisions/definitions, revisions, releases, tracks, and the package through the transaction repo. Explicit row deletion preserves memory-backend parity; PostgreSQL/SQLite cascades remain harmless. Delete `deleteOCIPackage`, `deleteAllResourceArtifacts`, `deleteAllRevisions`, `deleteAllReleases`, and `deleteAllTracks` after all callers are gone.

Pass the active lease into both zero-rule cleanup and final-rule deletion. Delete sync rules only after package cleanup commits, and execute each `DeleteCharmhubSyncRule` through `lease.WithinTransaction`; tolerate only `repo.ErrNotFound`. Conditional status updates from Task 2 prevent an in-flight completion from resurrecting their state.

- [ ] **Step 7: Run pruning, cleanup, and wildcard regression tests**

Run:

```bash
make fmt
go test ./internal/repo -run 'Test(PostgresDeleteStaleTrackReleasesUsesJSONBaseAndEscapedTrack|SQLiteDeleteStaleTrackReleasesEscapesWildcards)' -count=1
go test ./internal/sync -run 'Test(PruneEmptyTrack|PruneSyncedPackage|CleanupSyncedPackage|RemovingOneTrackPrunesOnlyUnreferencedArtifacts|CharmhubSyncRemovesChannelWhenUpstreamDisappears)' -count=1
```

Expected: all selected tests pass. `RemovingOneTrackPrunesOnlyUnreferencedArtifacts` now asserts cleanup jobs rather than immediate `trackingOCIRegistry.deletedImages` entries.

- [ ] **Step 8: Commit the pruning slice**

```bash
git add internal/sync/prune.go internal/sync/prune_test.go internal/sync/reconcile.go internal/sync/service_test.go
git commit -m "fix(sync): prune artifacts through durable reference-aware jobs"
```

---

### Task 6: Slice-Wide Verification and Dead-Code Gate

**Files:**
- Verify: all files changed in Tasks 1-5
- Modify only if verification exposes a slice-scoped defect: files already listed in this plan

**Interfaces:**
- Consumes: every interface produced above.
- Produces: a formatted, generated-code-clean, race-tested, buildable Charmhub sync hardening slice.

- [ ] **Step 1: Run focused invariant tests together**

Run:

```bash
go test ./internal/repo ./internal/sync -run 'ConditionalCharmhubRuleUpdate|DeleteStaleTrackReleases|SyncLease|CannotReconcileSamePackage|DeletingRuleIsNotOverwritten|RevisionStage|RepairsLegacyIncompleteRevision|ResourceReleaseStage|PruneEmptyTrack|PruneSyncedPackage|CleanupSyncedPackage' -count=1
```

Expected: PASS for both packages.

- [ ] **Step 2: Verify generated SQL and formatting**

Run:

```bash
go tool sqlc generate
make fmt
make sqlc-diff
git diff --check
```

Expected: `make sqlc-diff` and `git diff --check` exit zero; regeneration creates no unreviewed diff.

- [ ] **Step 3: Run package and race verification**

Run:

```bash
go test ./internal/repo ./internal/sync -count=1
go test -race ./internal/repo ./internal/sync -count=1
```

Expected: PASS with no race reports.

- [ ] **Step 4: Run the repository-wide unit, vet, lint, and build gates**

Run:

```bash
make test
make vet
make lint
make build
```

Expected: every command exits zero. These commands exclude integration suites and sqlc's generated package according to the Makefile.

- [ ] **Step 5: Confirm no obsolete sync mutation path remains**

Run:

```bash
rg -n 'UpdateCharmhubSyncRule\(|cleanupFailedCharmhubRevisionImport|deleteEmptyResourceDefinitions|deleteResourceArtifact|deleteOCIPackage|deleteAllResourceArtifacts|deleteAllRevisions|deleteAllReleases|deleteAllTracks|s\.oci\.Delete(Image|Package)' internal/sync
```

Expected: no matches. Any match is a blocker: route status through compare-and-set and destructive committed-metadata cleanup through jobs. `blob.Store.Delete` remains allowed only in the explicit pre-commit compensation helpers in `import_stages.go`.

- [ ] **Step 6: Review the final diff for scope and migration order**

Run:

```bash
git status --short
git diff --stat HEAD~5
git diff -- internal/repo/sqlite/migrations/0006_charmhub_sync_leases.sql internal/repo/queries/charmhub_sync.sql internal/repo/queries/releases.sql internal/sync
```

Expected: only the files named in this plan plus sqlc-generated outputs are changed; SQLite migration `0006` follows the prerequisite `0004` and `0005`; there is no PostgreSQL lease table migration.

- [ ] **Step 7: Commit final verification-only corrections if needed**

If Steps 1-6 required a slice-scoped correction, commit only those reviewed files:

```bash
git add internal/repo internal/sync
git commit -m "test(sync): complete hardening verification"
```

If no correction was required, do not create an empty commit.
