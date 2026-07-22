# Operations and Performance Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bound package search work and make database cleanup, OCI self-connectivity, process shutdown, CLI cancellation, and Charmhub redirects deterministic and safe.

**Architecture:** Add a repository search read model that joins the package, selected default release, and selected revision before applying pagination. Keep operational fixes at their existing boundaries: repository transaction helpers own rollback, configuration owns the embedded OCI URL invariant, the command owns listener coordination, the CLI owns request and whole-operation deadlines, and the Charmhub client owns redirect policy.

**Tech Stack:** Go 1.26.0 with toolchain 1.26.4, PostgreSQL with pgx/sqlc, modernc SQLite, `net/http`, and testify unit tests.

## Global Constraints

- Follow test-driven development: add one focused failing unit test, run it and observe the stated failure, implement only the behavior under test, then rerun it.
- Do not inspect or run integration or functional suites for this slice; use unit tests under `cmd/` and `internal/` only.
- Preserve the existing search response as a top-level object whose only field is the `results` array (the concrete empty form is `{ "results": [] }`); do not add pagination metadata.
- Search defaults to `limit=100`, accepts `offset=0`, and rejects limits outside `1..500` or negative offsets with HTTP 400; every repository backend independently enforces the same hard cap.
- Apply coarse public/owner/ACL visibility plus nonempty token package and channel selectors before SQL `LIMIT`/`OFFSET`; repeat authoritative service authorization after rows are loaded.
- Keep the existing default-stable release selection and fallback-to-latest-by-`when_created` semantics.
- Do not add third-party dependencies or schema migrations in this slice.
- Regenerate `internal/repo/db` only with `go tool sqlc generate`; never hand-edit generated files.
- Install rollback immediately after every touched `BeginTx`; never recover callback panics.
- PostgreSQL rollback and advisory unlock use `context.WithoutCancel` plus a five-second timeout.
- Default the CLI request timeout to 30 seconds and make it configurable with `--request-timeout` or `CHARM_REGISTRYCTL_REQUEST_TIMEOUT`.
- Preserve explicit OCI internal URLs; derive only an absent URL from the embedded listener and TLS files.
- HTTP-to-HTTP redirects remain compatible, but any request whose first hop used HTTPS may redirect only to HTTPS.
- `main` is the only function allowed to call `os.Exit`; all application closers must finish before it does.
- Preserve unrelated working-tree changes and avoid cosmetic file moves.
- End every task with the exact focused tests passing and a small commit.

---

## File Structure

- `internal/repo/interface.go`: owns `PackageSearchOptions`, `PackageSearchItem`, and a focused `PackageSearchRepo` contract; Task 3 embeds it into `PackageRepo` only after all three backends implement it.
- `internal/repo/memory.go`: deterministic in-memory implementation of the joined search read model.
- `internal/repo/sqlite_search.go`: focused SQLite joined search query and row scanner; keep this out of the already-large `sqlite.go`.
- `internal/repo/queries/packages.sql`: PostgreSQL joined search query consumed by sqlc.
- `internal/repo/postgres_packages.go`: PostgreSQL search option conversion and generated-query call.
- `internal/repo/postgres_sqlc.go`: maps the generated search row into the repository projection.
- `internal/repo/db/packages.sql.go`, `internal/repo/db/querier.go`: regenerated sqlc output.
- `internal/service/authorization.go`: exposes the existing centralized package policy as a repository-free evaluator for preloaded search visibility facts.
- `internal/service/packages.go`: builds search constraints, repeats slice 1's centralized policy through `authorizePackageWithRole(identity, pkg, packagePolicyForChannel(publicCompositePolicy, release.Channel), role)`, and maps the joined projection without per-result repository calls.
- `internal/api/http_releases.go`: parses `limit` and `offset` for `/v2/charms/find`.
- `internal/repo/postgres.go`, `internal/repo/sqlite.go`: immediate deferred rollback and detached PostgreSQL cleanup.
- `internal/config/config.go`: derived OCI internal URL and loopback TLS-mode validation.
- `cmd/charm-registry/main.go`: error-returning runtime and coordinated listener shutdown.
- `cmd/charm-registryctl/main.go`: signal-derived root context, dedicated client, request timeout, and whole-wait context.
- `internal/charmhub/client.go`: redirect downgrade check.
- Existing focused `_test.go` files plus new `internal/repo/postgres_transaction_test.go` define the behavior; no integration test files change.

### Task 1: Define the Joined Search Contract and Memory Behavior

**Files:**
- Modify: `internal/repo/interface.go`
- Modify: `internal/repo/memory.go`
- Test: `internal/repo/memory_test.go`

**Interfaces:**
- Consumes: existing `core.Package`, `core.Release`, `core.Revision`, slice 1's `core.PackageRole`, in-memory package ACL data, releases, and revisions.
- Produces: `repo.MaxPackageSearchLimit`, `repo.PackageSearchOptions`, `repo.PackageSearchItem`, and focused `PackageSearchRepo.SearchPackageSummaries(context.Context, PackageSearchOptions) ([]PackageSearchItem, error)` for Tasks 2-4.

- [ ] **Step 1: Add a failing memory test for visibility, selectors, default release, ordering, limit, and offset**

Append this complete fixture helper and test to `internal/repo/memory_test.go`:

```go
func seedMemorySearchPackage(
	t *testing.T,
	repository *Memory,
	pkg core.Package,
	revisions []core.Revision,
	releases []core.Release,
) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, repository.CreatePackage(ctx, pkg))
	for _, revision := range revisions {
		revision.PackageID = pkg.ID
		require.NoError(t, repository.CreateRevision(ctx, revision))
	}
	for _, release := range releases {
		release.PackageID = pkg.ID
		require.NoError(t, repository.ReplaceRelease(ctx, pkg.ID, release))
	}
}

func TestMemorySearchPackageSummaries(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemory()
	now := time.Unix(1_700_000_000, 0).UTC()
	publisher, err := repository.EnsureAccount(ctx, core.Account{
		ID: "publisher", Subject: "publisher-sub", Username: "publisher",
		DisplayName: "Publisher", Email: "publisher@example.com", Validation: "verified", CreatedAt: now,
	})
	require.NoError(t, err)
	caller, err := repository.EnsureAccount(ctx, core.Account{
		ID: "caller", Subject: "caller-sub", Username: "caller",
		DisplayName: "Caller", Email: "caller@example.com", Validation: "verified", CreatedAt: now,
	})
	require.NoError(t, err)
	track := "2.0"
	seedMemorySearchPackage(t, repository, core.Package{
		ID: "pkg-alpha", Name: "alpha", Type: "charm", Status: "published",
		OwnerAccountID: publisher.ID, DefaultTrack: &track, CreatedAt: now, UpdatedAt: now,
	}, []core.Revision{
		{ID: "rev-alpha-1", Revision: 1, Version: "1", CreatedAt: now, SHA256: "alpha-1"},
		{ID: "rev-alpha-2", Revision: 2, Version: "2", CreatedAt: now.Add(time.Minute), SHA256: "alpha-2"},
	}, []core.Release{
		{ID: "rel-alpha-stable", Channel: "2.0/stable", Revision: 2, When: now.Add(time.Minute)},
		{ID: "rel-alpha-edge", Channel: "2.0/edge", Revision: 1, When: now.Add(2 * time.Minute)},
	})
	seedMemorySearchPackage(t, repository, core.Package{
		ID: "pkg-beta", Name: "beta", Type: "charm", Private: true, Status: "published",
		OwnerAccountID: publisher.ID, CreatedAt: now, UpdatedAt: now,
	}, []core.Revision{
		{ID: "rev-beta-1", Revision: 1, Version: "1", CreatedAt: now, SHA256: "beta-1"},
	}, []core.Release{
		{ID: "rel-beta-stable", Channel: "latest/stable", Revision: 1, When: now},
	})
	repository.AddACLEntry("pkg-beta", "account", caller.ID, "viewer")
	seedMemorySearchPackage(t, repository, core.Package{
		ID: "pkg-gamma", Name: "gamma", Type: "charm", Status: "published",
		OwnerAccountID: publisher.ID, CreatedAt: now, UpdatedAt: now,
	}, []core.Revision{
		{ID: "rev-gamma-1", Revision: 1, Version: "1", CreatedAt: now, SHA256: "gamma-1"},
	}, []core.Release{
		{ID: "rel-gamma-stable", Channel: "latest/stable", Revision: 1, When: now},
	})
	seedMemorySearchPackage(t, repository, core.Package{
		ID: "pkg-delta", Name: "delta", Type: "charm", Status: "registered",
		OwnerAccountID: publisher.ID, CreatedAt: now, UpdatedAt: now,
	}, nil, nil)

	items, err := repository.SearchPackageSummaries(ctx, PackageSearchOptions{
		Query: "a", AccountID: caller.ID, RestrictPackages: true,
		PackageNames: []string{"alpha", "beta"}, Limit: 1, Offset: 1,
	})
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "beta", items[0].Package.Name)
	assert.Equal(t, "latest/stable", items[0].Release.Channel)
	assert.Equal(t, items[0].Release.Revision, items[0].Revision.Revision)

	items, err = repository.SearchPackageSummaries(ctx, PackageSearchOptions{
		Query: "alpha", AccountID: caller.ID, Limit: 1, Offset: 0,
	})
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "2.0/stable", items[0].Release.Channel)
	assert.Equal(t, 2, items[0].Revision.Revision)

	items, err = repository.SearchPackageSummaries(ctx, PackageSearchOptions{
		Query: "alpha", RestrictChannels: true,
		ChannelNames: []string{"2.0/edge"}, Limit: 1, Offset: 0,
	})
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "2.0/edge", items[0].Release.Channel)
	assert.Equal(t, 1, items[0].Revision.Revision)

	items, err = repository.SearchPackageSummaries(ctx, PackageSearchOptions{
		Query: "a", Limit: 10, Offset: 0,
	})
	require.NoError(t, err)
	require.Len(t, items, 2)
	assert.Equal(t, []string{"alpha", "gamma"}, []string{
		items[0].Package.Name, items[1].Package.Name,
	})

	_, err = repository.SearchPackageSummaries(ctx, PackageSearchOptions{
		Limit: MaxPackageSearchLimit + 1,
	})
	assert.ErrorContains(t, err, "limit must be between 1 and 500")
}
```

- [ ] **Step 2: Run the memory test and verify the contract is missing**

Run: `go test ./internal/repo -run TestMemorySearchPackageSummaries -count=1`

Expected: FAIL to compile with `repository.SearchPackageSummaries undefined` and `undefined: PackageSearchOptions`.

- [ ] **Step 3: Add the exact repository types and method**

Add these declarations beside `ReleaseVariant`/the repository interfaces in `internal/repo/interface.go`:

```go
const MaxPackageSearchLimit = 500

type PackageSearchOptions struct {
	Query            string
	AccountID        string
	IncludePrivate   bool
	RestrictPackages bool
	PackageIDs       []string
	PackageNames     []string
	RestrictChannels bool
	ChannelNames     []string
	Limit            int
	Offset           int
}

type PackageSearchItem struct {
	Package  core.Package
	Release  core.Release
	Revision core.Revision
}
```

Add a separate focused interface; do not embed it into `PackageRepo` yet, because SQLite and PostgreSQL are implemented in Tasks 2 and 3 and every intermediate commit must build:

```go
type PackageSearchRepo interface {
	SearchPackageSummaries(ctx context.Context, options PackageSearchOptions) ([]PackageSearchItem, error)
}
```

Keep the existing `PackageRepo.SearchPackages`, which is still used by the administrative package list. Task 3 embeds `PackageSearchRepo` only after every backend has the concrete method.

Add the shared repository guard and call it before every backend builds or executes a query:

```go
func validatePackageSearchOptions(options PackageSearchOptions) error {
	if options.Limit < 1 || options.Limit > MaxPackageSearchLimit {
		return fmt.Errorf("limit must be between 1 and %d", MaxPackageSearchLimit)
	}
	if options.Offset < 0 {
		return fmt.Errorf("offset must be greater than or equal to zero")
	}
	return nil
}
```

Each backend returns an empty non-nil slice without querying when `RestrictPackages` is true with no IDs/names or when `RestrictChannels` is true with no channel names.

- [ ] **Step 4: Implement the deterministic in-memory projection**

Implement `Memory.SearchPackageSummaries` under one `RLock`. Call `validatePackageSearchOptions` first. Filter names with `strings.Contains(strings.ToLower(pkg.Name), strings.ToLower(strings.TrimSpace(options.Query)))`. A row is coarsely visible when the package is public, `IncludePrivate` is true, the account owns it, or a matching direct ACL entry parses with `core.ParsePackageRole` to at least `core.PackageRoleViewer`. When `RestrictPackages` is true, require `slices.Contains(options.PackageIDs, pkg.ID) || slices.Contains(options.PackageNames, pkg.Name)`.

For each remaining package, first discard releases outside `ChannelNames` when `RestrictChannels` is true. Derive `stableChannel` as `"latest/stable"` or `*pkg.DefaultTrack + "/stable"`; choose the newest remaining release on that channel by `When`, breaking equal timestamps by ascending release ID. If no allowed stable release exists, choose the newest remaining release across all channels with the same tie-break. Skip packages with no allowed release or no matching revision. Sort `PackageSearchItem` values by package name then package ID, then apply offset and limit. Return `[]PackageSearchItem{}` rather than nil when no row survives or the offset is beyond the end. Do not call another `Memory` method while holding the lock.

The coarse ACL check must match slice 1: direct account ACLs and group ACLs through `Memory.groupMembers` both qualify when their parsed role is at least viewer.

- [ ] **Step 5: Run the memory and package tests**

Run: `go test ./internal/repo -run 'TestMemorySearchPackageSummaries|TestRepositorySearchPackagesEscapesWildcards' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit the contract and memory implementation**

```bash
git add internal/repo/interface.go internal/repo/memory.go internal/repo/memory_test.go
git commit -m "feat: add bounded package search projection"
```

### Task 2: Implement the SQLite Joined Search Query

**Files:**
- Create: `internal/repo/sqlite_search.go`
- Test: `internal/repo/sqlite_test.go`

**Interfaces:**
- Consumes: `PackageSearchOptions` and `PackageSearchItem` from Task 1, `SQLite.db`, `escapeLikePattern`, `inQuery`, and JSON/null helpers from `internal/repo`.
- Produces: `(*SQLite).SearchPackageSummaries(context.Context, PackageSearchOptions) ([]PackageSearchItem, error)`.

- [ ] **Step 1: Add a failing SQLite behavior test**

Create `TestSQLiteSearchPackageSummariesIsJoinedBoundedAndVisible` using `newSQLiteTestRepository`. Insert public, caller-owned private, inaccessible private, and unreleased packages. Insert multiple releases for the public package so its configured stable release is older than an edge release. Insert matching revision rows. Assert:

```go
items, err := repository.SearchPackageSummaries(ctx, PackageSearchOptions{
	Query:            "search-",
	AccountID:        caller.ID,
	RestrictPackages: true,
	PackageNames:     []string{"search-inaccessible", "search-owned", "search-public"},
	Limit:            1,
	Offset:           1,
})
require.NoError(t, err)
require.Len(t, items, 1)
assert.Equal(t, "search-public", items[0].Package.Name)
assert.Equal(t, "2.0/stable", items[0].Release.Channel)
assert.Equal(t, 2, items[0].Revision.Revision)
```

Then query anonymously with `Limit: 10` and assert that only released public packages appear. This one test proves visibility and selectors are applied before pagination because `search-inaccessible` sorts before `search-public` but cannot consume the requested slot.

Finally restrict channels to the public package's edge release and assert that edge is selected even though stable is the global default. This proves channel scope is applied inside release ranking and before pagination.

- [ ] **Step 2: Run the SQLite test and observe the missing method**

Run: `go test ./internal/repo -run TestSQLiteSearchPackageSummariesIsJoinedBoundedAndVisible -count=1`

Expected: FAIL to compile because `*SQLite` does not implement `SearchPackageSummaries`.

- [ ] **Step 3: Implement the focused SQLite query and scanner**

Create `internal/repo/sqlite_search.go`. Use this complete query shape, adding selector `IN` clauses before the final order when `RestrictPackages` is true:

```sql
WITH ranked_releases AS (
    SELECT r.id, r.package_id, r.channel, r.revision, r.base, r.when_created,
           ROW_NUMBER() OVER (
               PARTITION BY r.package_id
               ORDER BY
                   CASE
                       WHEN r.channel = COALESCE(p.default_track, 'latest') || '/stable' THEN 0
                       ELSE 1
                   END,
                   r.when_created DESC,
                   r.id ASC
           ) AS release_rank
    FROM releases r
    JOIN packages p ON p.id = r.package_id
    WHERE (NOT ? OR r.channel IN (
        SELECT CAST(value AS TEXT) FROM json_each(?)
    ))
)
SELECT p.id, p.name, p.type, p.private, p.status, p.owner_account_id,
       p.description, p.summary, p.title, p.website, p.links, p.media,
       p.created_at, p.updated_at,
       a.id, a.username, a.display_name, a.email, a.validation,
       r.id, r.package_id, r.channel, r.revision, r.base, r.when_created,
       v.package_id, v.revision, v.version, v.created_at, v.size,
       v.sha256, v.bases, v.attributes
FROM packages p
JOIN accounts a ON a.id = p.owner_account_id
JOIN ranked_releases r ON r.package_id = p.id AND r.release_rank = 1
JOIN revisions v ON v.package_id = p.id AND v.revision = r.revision
WHERE p.name LIKE ? ESCAPE '\' COLLATE NOCASE
  AND (
      ?
      OR NOT p.private
      OR p.owner_account_id = ?
      OR EXISTS (
          SELECT 1
          FROM package_acl acl
          LEFT JOIN account_group_members gm
            ON acl.principal_type = 'group' AND acl.principal_id = gm.group_id
          WHERE acl.package_id = p.id
            AND acl.role IN ('viewer', 'editor', 'owner')
            AND (
                (acl.principal_type = 'account' AND acl.principal_id = ?)
                OR gm.account_id = ?
            )
      )
  )
```

Call `validatePackageSearchOptions` before building SQL. Encode `options.ChannelNames` with `rawJSONString` and build the base argument list in this exact order: `RestrictChannels`, channel JSON, escaped name pattern, `IncludePrivate`, then the account ID three times. `json_each` is built into the repository's modern SQLite version. When package restriction is true and both selector slices are empty, or channel restriction is true with no channel names, return `[]PackageSearchItem{}` without querying. Otherwise insert the package selector predicate before `ORDER BY` with this exact OR behavior, then append limit and offset:

```go
selectorClauses := make([]string, 0, 2)
if len(options.PackageIDs) > 0 {
	clause, selectorArgs := inQuery("p.id IN (%s)", options.PackageIDs)
	selectorClauses = append(selectorClauses, clause)
	args = append(args, selectorArgs...)
}
if len(options.PackageNames) > 0 {
	clause, selectorArgs := inQuery("p.name IN (%s)", options.PackageNames)
	selectorClauses = append(selectorClauses, clause)
	args = append(args, selectorArgs...)
}
if options.RestrictPackages {
	query += " AND (" + strings.Join(selectorClauses, " OR ") + ")"
}
query += " ORDER BY p.name ASC, p.id ASC LIMIT ? OFFSET ?"
args = append(args, options.Limit, options.Offset)
```

The scanner must populate only the fields selected above, convert nullable package metadata with `nullStringPtr`, decode package links/media, release base, and revision bases/attributes with `unmarshalJSON`, and return `rows.Err()` after iteration. Do not call another repository method from the row loop.

- [ ] **Step 4: Run focused SQLite and repository tests**

Run: `go test ./internal/repo -run 'TestSQLiteSearchPackageSummariesIsJoinedBoundedAndVisible|TestMemorySearchPackageSummaries' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the SQLite read model**

```bash
git add internal/repo/sqlite_search.go internal/repo/sqlite_test.go
git commit -m "feat: add sqlite package search read model"
```

### Task 3: Generate and Map the PostgreSQL Joined Search Query

**Files:**
- Modify: `internal/repo/interface.go`
- Modify: `internal/repo/queries/packages.sql`
- Modify: `internal/repo/postgres_packages.go`
- Modify: `internal/repo/postgres_sqlc.go`
- Regenerate: `internal/repo/db/packages.sql.go`
- Regenerate: `internal/repo/db/querier.go`
- Test: `internal/repo/postgres_scan_test.go`

**Interfaces:**
- Consumes: `PackageSearchOptions` and `PackageSearchItem` from Task 1 plus `escapeLikePattern`, `toInt32`, and JSON helpers.
- Produces: PostgreSQL `SearchPackageSummaries` with the same filtering, selection, ordering, and pagination semantics as SQLite, then embeds the now-fully-implemented `PackageSearchRepo` into `PackageRepo`.

- [ ] **Step 1: Add a failing PostgreSQL projection mapping test**

Replace the `postgres_scan_test.go` import block with this exact block, then append the complete test below it:

```go
import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sqlcdb "github.com/gschiano/charm-registry/internal/repo/db"
)
```

```go
func TestPackageSearchItemFromSQLC(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0).UTC()
	description := "Operator framework charm"
	summary := "Demo summary"
	title := "Demo"
	website := "https://example.com/demo"

	item, err := packageSearchItemFromSQLC(sqlcdb.SearchPackageSummariesRow{
		PackageID:            "pkg-1",
		PackageName:          "demo",
		PackageType:          "charm",
		PackagePrivate:       false,
		PackageStatus:        "published",
		OwnerAccountID:       "acc-1",
		PackageDescription:   &description,
		PackageSummary:       &summary,
		PackageTitle:         &title,
		PackageWebsite:       &website,
		PackageLinks:         json.RawMessage(`{"issues":["https://example.com/issues"]}`),
		PackageMedia:         json.RawMessage(`[{"type":"icon","url":"https://example.com/icon.png"}]`),
		PackageCreatedAt:     now,
		PackageUpdatedAt:     now.Add(time.Minute),
		PublisherID:          "acc-1",
		PublisherUsername:    "publisher",
		PublisherDisplayName: "Publisher",
		PublisherEmail:       "publisher@example.com",
		PublisherValidation:  "verified",
		ReleaseID:            "rel-1",
		ReleasePackageID:     "pkg-1",
		ReleaseChannel:       "2.0/stable",
		ReleaseRevision:      2,
		ReleaseBase:          json.RawMessage(`{"name":"ubuntu","channel":"24.04","architecture":"amd64"}`),
		ReleaseWhenCreated:   now.Add(2 * time.Minute),
		RevisionPackageID:    "pkg-1",
		RevisionNumber:       2,
		RevisionVersion:      "2.0.0",
		RevisionCreatedAt:    now.Add(time.Minute),
		RevisionSize:         42,
		RevisionSha256:       "deadbeef",
		RevisionBases:        json.RawMessage(`[{"name":"ubuntu","channel":"24.04","architecture":"amd64"}]`),
		RevisionAttributes:   json.RawMessage(`{"framework":"operator"}`),
	})
	require.NoError(t, err)
	assert.Equal(t, "demo", item.Package.Name)
	assert.Equal(t, description, *item.Package.Description)
	assert.Equal(t, []string{"https://example.com/issues"}, item.Package.Links["issues"])
	require.Len(t, item.Package.Media, 1)
	assert.Equal(t, "publisher", item.Package.Publisher.Username)
	assert.Equal(t, "2.0/stable", item.Release.Channel)
	require.NotNil(t, item.Release.Base)
	assert.Equal(t, "amd64", item.Release.Base.Architecture)
	assert.Equal(t, 2, item.Revision.Revision)
	assert.Equal(t, "deadbeef", item.Revision.SHA256)
	require.Len(t, item.Revision.Bases, 1)
	assert.Equal(t, "ubuntu", item.Revision.Bases[0].Name)
	assert.Equal(t, "operator", item.Revision.Attributes["framework"])
}
```

- [ ] **Step 2: Run the mapping test and verify generated symbols are absent**

Run: `go test ./internal/repo -run TestPackageSearchItemFromSQLC -count=1`

Expected: FAIL to compile with `undefined: sqlcdb.SearchPackageSummariesRow` and `undefined: packageSearchItemFromSQLC`.

- [ ] **Step 3: Add the PostgreSQL query**

Append this named query to `internal/repo/queries/packages.sql`:

```sql
-- name: SearchPackageSummaries :many
SELECT
    p.id AS package_id,
    p.name AS package_name,
    p.type AS package_type,
    p.private AS package_private,
    p.status AS package_status,
    p.owner_account_id,
    p.description AS package_description,
    p.summary AS package_summary,
    p.title AS package_title,
    p.website AS package_website,
    p.links AS package_links,
    p.media AS package_media,
    p.created_at AS package_created_at,
    p.updated_at AS package_updated_at,
    a.id AS publisher_id,
    a.username AS publisher_username,
    a.display_name AS publisher_display_name,
    a.email AS publisher_email,
    a.validation AS publisher_validation,
    selected_release.id AS release_id,
    selected_release.package_id AS release_package_id,
    selected_release.channel AS release_channel,
    selected_release.revision AS release_revision,
    selected_release.base AS release_base,
    selected_release.when_created AS release_when_created,
    rev.package_id AS revision_package_id,
    rev.revision AS revision_number,
    rev.version AS revision_version,
    rev.created_at AS revision_created_at,
    rev.size AS revision_size,
    rev.sha256 AS revision_sha256,
    rev.bases AS revision_bases,
    rev.attributes AS revision_attributes
FROM packages p
JOIN accounts a ON a.id = p.owner_account_id
JOIN LATERAL (
    SELECT r.id, r.package_id, r.channel, r.revision, r.base, r.when_created
    FROM releases r
    WHERE r.package_id = p.id
      AND (
          NOT sqlc.arg(restrict_channels)::boolean
          OR r.channel = ANY(sqlc.arg(channel_names)::text[])
      )
    ORDER BY
        CASE
            WHEN r.channel = COALESCE(p.default_track, 'latest') || '/stable' THEN 0
            ELSE 1
        END,
        r.when_created DESC,
        r.id ASC
    LIMIT 1
) selected_release ON TRUE
JOIN revisions rev
  ON rev.package_id = p.id
 AND rev.revision = selected_release.revision
WHERE p.name ILIKE sqlc.arg(name_pattern)::text ESCAPE '\'
  AND (
      sqlc.arg(include_private)::boolean
      OR NOT p.private
      OR p.owner_account_id = sqlc.arg(account_id)::text
      OR EXISTS (
          SELECT 1
          FROM package_acl acl
          LEFT JOIN account_group_members gm
            ON acl.principal_type = 'group' AND acl.principal_id = gm.group_id
          WHERE acl.package_id = p.id
            AND acl.role = ANY(ARRAY['viewer', 'editor', 'owner']::text[])
            AND (
                (acl.principal_type = 'account' AND acl.principal_id = sqlc.arg(account_id)::text)
                OR gm.account_id = sqlc.arg(account_id)::text
            )
      )
  )
  AND (
      NOT sqlc.arg(restrict_packages)::boolean
      OR p.id = ANY(sqlc.arg(package_ids)::text[])
      OR p.name = ANY(sqlc.arg(package_names)::text[])
  )
ORDER BY p.name ASC, p.id ASC
LIMIT sqlc.arg(result_limit)::int
OFFSET sqlc.arg(result_offset)::int;
```

- [ ] **Step 4: Generate the deterministic sqlc types**

Run: `go tool sqlc generate`

Expected: PASS and update only `internal/repo/db/packages.sql.go` and `internal/repo/db/querier.go` for this query. With the aliases above, sqlc deterministically generates `SearchPackageSummariesParams` fields `RestrictChannels`, `ChannelNames`, `NamePattern`, `IncludePrivate`, `AccountID`, `RestrictPackages`, `PackageIds`, `PackageNames`, `ResultOffset`, and `ResultLimit`; it generates the `SearchPackageSummariesRow` fields used verbatim in Step 1, including `ReleaseBase` and `RevisionSha256`.

Run: `go tool sqlc diff`

Expected: PASS with no generated-code drift.

- [ ] **Step 5: Implement PostgreSQL option conversion**

Add this method to `internal/repo/postgres_packages.go`:

```go
func (p *Postgres) SearchPackageSummaries(
	ctx context.Context,
	options PackageSearchOptions,
) ([]PackageSearchItem, error) {
	if err := validatePackageSearchOptions(options); err != nil {
		return nil, err
	}
	if (options.RestrictPackages && len(options.PackageIDs) == 0 && len(options.PackageNames) == 0) ||
		(options.RestrictChannels && len(options.ChannelNames) == 0) {
		return []PackageSearchItem{}, nil
	}
	limit, err := toInt32(options.Limit)
	if err != nil {
		return nil, err
	}
	offset, err := toInt32(options.Offset)
	if err != nil {
		return nil, err
	}
	pattern := "%"
	if query := strings.TrimSpace(options.Query); query != "" {
		pattern = "%" + escapeLikePattern(query) + "%"
	}
	rows, err := p.queries().SearchPackageSummaries(ctx, sqlcdb.SearchPackageSummariesParams{
		RestrictChannels: options.RestrictChannels,
		ChannelNames:     options.ChannelNames,
		NamePattern:      pattern,
		IncludePrivate:   options.IncludePrivate,
		AccountID:        options.AccountID,
		RestrictPackages: options.RestrictPackages,
		PackageIds:       options.PackageIDs,
		PackageNames:     options.PackageNames,
		ResultOffset:     offset,
		ResultLimit:      limit,
	})
	if err != nil {
		return nil, err
	}
	items := make([]PackageSearchItem, 0, len(rows))
	for _, row := range rows {
		item, err := packageSearchItemFromSQLC(row)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}
```

- [ ] **Step 6: Implement the complete PostgreSQL row mapper**

Add this converter to `internal/repo/postgres_sqlc.go`:

```go
func packageSearchItemFromSQLC(row sqlcdb.SearchPackageSummariesRow) (PackageSearchItem, error) {
	item := PackageSearchItem{
		Package: core.Package{
			ID: row.PackageID, Name: row.PackageName, Type: row.PackageType,
			Private: row.PackagePrivate, Status: row.PackageStatus,
			OwnerAccountID: row.OwnerAccountID, Description: row.PackageDescription,
			Summary: row.PackageSummary, Title: row.PackageTitle, Website: row.PackageWebsite,
			Publisher: core.Publisher{
				ID: row.PublisherID, Username: row.PublisherUsername,
				DisplayName: row.PublisherDisplayName, Email: row.PublisherEmail,
				Validation: row.PublisherValidation,
			},
			CreatedAt: row.PackageCreatedAt, UpdatedAt: row.PackageUpdatedAt,
		},
		Release: core.Release{
			ID: row.ReleaseID, PackageID: row.ReleasePackageID,
			Channel: row.ReleaseChannel, Revision: int(row.ReleaseRevision),
			When: row.ReleaseWhenCreated,
		},
		Revision: core.Revision{
			PackageID: row.RevisionPackageID, Revision: int(row.RevisionNumber),
			Version: row.RevisionVersion, CreatedAt: row.RevisionCreatedAt,
			Size: row.RevisionSize, SHA256: row.RevisionSha256,
		},
	}
	if err := unmarshalJSON(row.PackageLinks, &item.Package.Links); err != nil {
		return PackageSearchItem{}, fmt.Errorf("unmarshal package search links: %w", err)
	}
	if err := unmarshalJSON(row.PackageMedia, &item.Package.Media); err != nil {
		return PackageSearchItem{}, fmt.Errorf("unmarshal package search media: %w", err)
	}
	if string(row.ReleaseBase) != "null" && len(row.ReleaseBase) != 0 {
		var base core.Base
		if err := unmarshalJSON(row.ReleaseBase, &base); err != nil {
			return PackageSearchItem{}, fmt.Errorf("unmarshal package search release base: %w", err)
		}
		item.Release.Base = &base
	}
	if err := unmarshalJSON(row.RevisionBases, &item.Revision.Bases); err != nil {
		return PackageSearchItem{}, fmt.Errorf("unmarshal package search revision bases: %w", err)
	}
	if err := unmarshalJSON(row.RevisionAttributes, &item.Revision.Attributes); err != nil {
		return PackageSearchItem{}, fmt.Errorf("unmarshal package search revision attributes: %w", err)
	}
	return item, nil
}
```

After the PostgreSQL method and mapper exist, embed `PackageSearchRepo` into `PackageRepo` in `internal/repo/interface.go`. At this point Memory, SQLite, and PostgreSQL all satisfy the expanded composite contract, so the Task 3 commit remains buildable.

- [ ] **Step 7: Run mapping, SQL-generation, and repository tests**

Run: `go test ./internal/repo -run 'TestPackageSearchItemFromSQLC|TestSQLiteSearchPackageSummariesIsJoinedBoundedAndVisible|TestMemorySearchPackageSummaries' -count=1`

Expected: PASS.

Run: `go tool sqlc diff`

Expected: PASS.

- [ ] **Step 8: Commit the PostgreSQL read model and generated output**

```bash
git add internal/repo/interface.go internal/repo/queries/packages.sql internal/repo/postgres_packages.go internal/repo/postgres_sqlc.go internal/repo/db/packages.sql.go internal/repo/db/querier.go internal/repo/postgres_scan_test.go
git commit -m "feat: add postgres package search read model"
```

### Task 4: Wire Search Pagination Through Service and HTTP

**Files:**
- Modify: `internal/service/packages.go`
- Modify: `internal/service/authorization.go`
- Modify: `internal/service/service_test.go`
- Modify: `internal/api/http_releases.go`
- Modify: `internal/api/http_test.go`

**Interfaces:**
- Consumes: `PackageRepo.SearchPackageSummaries`, `PackageSearchOptions`, `PackageSearchItem`, slice 1's `packagePolicy`/`publicCompositePolicy`, and token package selectors.
- Produces: repository-free `(*Service).authorizePackageWithRole(core.Identity, core.Package, packagePolicy, core.PackageRole) error` plus `SearchPackagesPage(context.Context, core.Identity, string, int, int) (findResponse, error)` while retaining the existing three-argument `SearchPackages` wrapper.

- [ ] **Step 1: Add failing service tests for one-query mapping and offset**

Add a decorator in `service_test.go`:

```go
type countingSearchRepository struct {
	repo.Backend
	searchCalls int
	roleCalls   int
}

func (r *countingSearchRepository) SearchPackageSummaries(
	ctx context.Context,
	options repo.PackageSearchOptions,
) ([]repo.PackageSearchItem, error) {
	r.searchCalls++
	return r.Backend.SearchPackageSummaries(ctx, options)
}

func (r *countingSearchRepository) GetPackageRole(
	ctx context.Context,
	packageID, accountID string,
) (core.PackageRole, error) {
	r.roleCalls++
	return r.Backend.GetPackageRole(ctx, packageID, accountID)
}
```

Create three public released packages in a memory backend, wrap it, call `SearchPackagesPage(ctx, anonymous, "page-", 1, 1)`, and assert exactly one result, the second package by name, `searchCalls == 1`, and `roleCalls == 0`. Add a private group-viewer ACL case and keep `roleCalls == 0`; the bounded repository query already supplies the coarse fact that a normal non-owner private result has at least viewer access. Add a store-token case with one package selector and one channel restriction where stable is global default but edge is allowed. Assert both restrictions reach the repository options before limiting and the response contains only the selected package's edge release.

- [ ] **Step 2: Run the service test and verify the page method is missing**

Run: `go test ./internal/service -run TestSearchPackagesPageUsesSingleBoundedProjection -count=1`

Expected: FAIL to compile with `svc.SearchPackagesPage undefined`.

- [ ] **Step 3: Replace the N+1 service path with the projection**

Add:

```go
const (
	DefaultSearchLimit = 100
	MaxSearchLimit     = repo.MaxPackageSearchLimit
)
```

Keep compatibility for current internal callers:

```go
func (s *Service) SearchPackages(
	ctx context.Context,
	identity core.Identity,
	query string,
) (findResponse, error) {
	return s.SearchPackagesPage(ctx, identity, query, DefaultSearchLimit, 0)
}
```

First split slice 1's centralized evaluator without changing its decisions:

```go
func (s *Service) authorizePackageWithRole(
	identity core.Identity,
	pkg core.Package,
	policy packagePolicy,
	role core.PackageRole,
) error
```

Move the system, anonymous-public, token package/permission/channel, administrator, public-package, and `role.AtLeast` branches into that pure helper. Keep `(*Service).authorizePackage` as the wrapper that loads owner/ACL role with `GetPackageRole` and delegates. This is a move, not a second policy implementation.

Implement `SearchPackagesPage` to reject invalid pagination, derive `IncludePrivate` from system/admin identity, copy nonempty token package IDs/names into `PackageSearchOptions`, set `RestrictChannels` and copy `ChannelNames` when a token has channel restrictions, and call `SearchPackageSummaries` once. For each returned item, pass `PackageRoleOwner` when the caller owns it, `PackageRoleViewer` when it is private and the caller is a normal non-owner (the query admitted it only through a known direct/group ACL role), and `PackageRoleNone` otherwise to `authorizePackageWithRole(identity, item.Package, packagePolicyForChannel(publicCompositePolicy, item.Release.Channel), role)`. Handle its result exactly as follows so database/configuration failures are never hidden as filtered rows:

```go
options := repo.PackageSearchOptions{
	Query: query, AccountID: identity.Account.ID,
	IncludePrivate: identity.System || identity.Account.IsAdmin,
	Limit: limit, Offset: offset,
}
if identity.Token != nil {
	for _, selector := range identity.Token.Packages {
		if selector.ID != "" {
			options.PackageIDs = append(options.PackageIDs, selector.ID)
		}
		if selector.Name != "" {
			options.PackageNames = append(options.PackageNames, selector.Name)
		}
	}
	options.RestrictPackages = len(identity.Token.Packages) > 0
	options.RestrictChannels = len(identity.Token.Channels) > 0
	options.ChannelNames = append([]string(nil), identity.Token.Channels...)
}
```

```go
policy := packagePolicyForChannel(publicCompositePolicy, item.Release.Channel)
if err := s.authorizePackageWithRole(identity, item.Package, policy, role); err != nil {
	var policyErr *Error
	if errors.As(err, &policyErr) &&
		(policyErr.Kind == ErrorKindUnauthorized || policyErr.Kind == ErrorKindForbidden) {
		continue
	}
	return findResponse{}, err
}
```

This repeats the authoritative token policy without an ACL query per result. Set `Package.Store`, then map the joined item.

Change `packageFindResult` into a pure mapper accepting `repo.PackageSearchItem`; remove its calls to `ResolveDefaultRelease` and `GetRevisionByNumber`. Remove `enrichPackages` from the find path; it loaded tracks that the find response never uses.

- [ ] **Step 4: Run focused service tests**

Run: `go test ./internal/service -run 'TestSearchPackagesPageUsesSingleBoundedProjection|TestPackagePublishedSupportsInfoAndRefresh|TestPrivatePackagesRequireAuthentication|TestAnonymousPublicConsumerServiceAccess' -count=1`

Expected: PASS.

- [ ] **Step 5: Add failing HTTP pagination tests**

Extend `TestFindEndpoint` or add `TestFindEndpointValidatesPagination`. Assert:

```go
for _, path := range []string{
	"/v2/charms/find?limit=0",
	"/v2/charms/find?limit=501",
	"/v2/charms/find?limit=abc",
	"/v2/charms/find?offset=-1",
	"/v2/charms/find?offset=abc",
} {
	resp := doRequest(t, handler, http.MethodGet, path, nil, "")
	assert.Equal(t, http.StatusBadRequest, resp.Code, path)
}
```

Publish three matching public packages, request `limit=1&offset=1`, and assert one result with the second name while the top-level JSON still has only `results`.

- [ ] **Step 6: Run the HTTP test and observe invalid values are accepted**

Run: `go test ./internal/api -run TestFindEndpointValidatesPagination -count=1`

Expected: FAIL because the handler currently ignores both parameters and returns HTTP 200.

- [ ] **Step 7: Parse and pass `limit`/`offset`**

Add a small `parseFindPagination(url.Values) (int, int, error)` helper in `http_releases.go`. Missing values use 100 and 0. Use `strconv.Atoi`; reject limit below 1, above 500, or offset below zero with messages `limit must be between 1 and 500` and `offset must be greater than or equal to zero`. `handleFind` converts parse errors with `invalidRequestError` and calls `SearchPackagesPage`.

- [ ] **Step 8: Run API, service, and repository packages**

Run: `go test ./internal/api ./internal/service ./internal/repo -count=1`

Expected: PASS.

- [ ] **Step 9: Commit the public pagination behavior**

```bash
git add internal/service/authorization.go internal/service/packages.go internal/service/service_test.go internal/api/http_releases.go internal/api/http_test.go
git commit -m "feat: expose bounded package search pagination"
```

### Task 5: Roll Back Panicking Transactions and Safely Release Migration Locks

**Files:**
- Modify: `internal/repo/postgres.go`
- Modify: `internal/repo/sqlite.go`
- Create: `internal/repo/postgres_transaction_test.go`
- Modify: `internal/repo/sqlite_test.go`

**Interfaces:**
- Consumes: `postgresDB`, `pgx.Tx`, `pgxpool.Conn`, and `Transactor.WithinTransaction`.
- Produces: `runPostgresTransaction(context.Context, postgresTransaction, func(postgresTransaction) error) error` and `cleanupMigrationAdvisoryLock` for both normal transactions and migrations.

- [ ] **Step 1: Add a failing SQLite panic rollback test**

Add this test to `internal/repo/sqlite_test.go`. The short lookup and ping contexts make the pre-fix leaked sole connection fail deterministically instead of hanging the package:

```go
func TestSQLiteTransactionRollsBackOnPanic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := newSQLiteTestRepository(t)
	owner := ensureSQLiteAccount(t, repository, "panic-owner", "panic-owner")
	now := time.Unix(1_700_000_000, 0).UTC()
	pkg := core.Package{
		ID: "panic-package", Name: "panic-package", Type: "charm", Private: true,
		Status: "registered", OwnerAccountID: owner.ID, CreatedAt: now, UpdatedAt: now,
	}

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_ = repository.WithinTransaction(ctx, func(transaction CompositeRepo) error {
			require.NoError(t, transaction.CreatePackage(ctx, pkg))
			panic("transaction panic")
		})
	}()
	assert.Equal(t, "transaction panic", recovered)

	lookupCtx, cancelLookup := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancelLookup()
	_, err := repository.GetPackageByName(lookupCtx, pkg.Name)
	assert.ErrorIs(t, err, ErrNotFound)

	pingCtx, cancelPing := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancelPing()
	assert.NoError(t, repository.Ping(pingCtx))
}
```

- [ ] **Step 2: Run the SQLite panic test and observe the leaked connection**

Run: `go test ./internal/repo -run TestSQLiteTransactionRollsBackOnPanic -count=1`

Expected: FAIL after about 100 ms: `GetPackageByName` returns `context deadline exceeded` rather than `ErrNotFound`, and the ping also times out because the panicking transaction still holds SQLite's sole connection. The row is not committed; this test detects resource leakage, not persistence.

- [ ] **Step 3: Install SQLite rollback immediately**

Immediately after `BeginTx`, add:

```go
defer func() {
	_ = tx.Rollback()
}()
```

Keep callback errors unchanged and commit only after a nil callback result. Do not add `recover`.

- [ ] **Step 4: Run the SQLite transaction tests**

Run: `go test ./internal/repo -run 'TestSQLiteTransactionRollsBackOnPanic|TestSQLiteTransactionRollsBack' -count=1`

Expected: PASS.

- [ ] **Step 5: Add failing PostgreSQL helper tests**

In `postgres_transaction_test.go`, define a recording transaction implementing `postgresDB`, `Commit`, and `Rollback`. Add:

```go
func TestRunPostgresTransactionRollsBackOnPanicWithDetachedContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tx := &recordingPostgresTransaction{}
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_ = runPostgresTransaction(ctx, tx, func(postgresTransaction) error {
			panic("transaction panic")
		})
	}()
	assert.Equal(t, "transaction panic", recovered)
	assert.Equal(t, 1, tx.rollbackCalls)
	assert.True(t, tx.rollbackContextWasLive)
	assert.Zero(t, tx.commitCalls)
}
```

The fake sets `rollbackContextWasLive = ctx.Err() == nil` inside `Rollback`; do not retain the context and inspect it after the helper returns, because a correct helper cancels its cleanup timeout before returning.

Add a callback-error case and a success case to assert one rollback attempt in both paths, with `pgx.ErrTxClosed` ignored after successful commit.

- [ ] **Step 6: Run the PostgreSQL helper test and verify the helper is absent**

Run: `go test ./internal/repo -run TestRunPostgresTransaction -count=1`

Expected: FAIL to compile with `undefined: runPostgresTransaction` and `undefined: postgresTransaction`.

- [ ] **Step 7: Implement and use the PostgreSQL transaction helper**

Add:

```go
const postgresCleanupTimeout = 5 * time.Second

type postgresTransaction interface {
	postgresDB
	Commit(context.Context) error
	Rollback(context.Context) error
}
```

`runPostgresTransaction` installs a defer before invoking the callback. The defer creates `context.WithTimeout(context.WithoutCancel(ctx), postgresCleanupTimeout)`, calls rollback, ignores `pgx.ErrTxClosed`, and joins other rollback errors to the named return error. A panic naturally runs the defer and continues unwinding. Use this helper from both `WithinTransaction` and each migration transaction.

- [ ] **Step 8: Add failing migration-lock cleanup tests**

Test `cleanupMigrationAdvisoryLock` with an already-canceled caller context. Cover:

1. unlock returns `(true, nil)`: discard is not called and unlock sees a live context;
2. unlock returns `(false, nil)`: discard is called once with a live context and the helper returns an error containing `advisory unlock returned false`;
3. unlock returns an error: discard is called and both unlock/discard errors remain discoverable with `errors.Is`.

- [ ] **Step 9: Run the migration cleanup tests and verify the helper is absent**

Run: `go test ./internal/repo -run TestCleanupMigrationAdvisoryLock -count=1`

Expected: FAIL to compile with `undefined: cleanupMigrationAdvisoryLock`.

- [ ] **Step 10: Implement detached unlock and connection discard**

Implement:

```go
func cleanupMigrationAdvisoryLock(
	ctx context.Context,
	unlock func(context.Context) (bool, error),
	discard func(context.Context) error,
) error
```

The helper derives one detached five-second context for `unlock`, cancels it as soon as unlock returns, and returns nil only for `(true, nil)`. For false or error it derives a fresh detached five-second context for `discard`; do not reuse a possibly expired unlock context. Join the uncertain-unlock and discard errors.

Make `Migrate` use a named return error. After acquiring the dedicated connection, defer `Release` while it remains pool-owned. After locking, defer `cleanupMigrationAdvisoryLock`; its unlock closure uses `QueryRow(cleanupCtx, "SELECT pg_advisory_unlock($1, $2)", lockClass, lockObjID).Scan(&unlocked)`. Its discard closure sets a local `poolOwned` flag false, calls `conn.Hijack()`, and closes the raw connection. The earlier release defer checks `poolOwned`, so a hijacked session is never returned to the pool.

- [ ] **Step 11: Run all repository transaction tests**

Run: `go test ./internal/repo -run 'TestSQLiteTransaction|TestRunPostgresTransaction|TestCleanupMigrationAdvisoryLock|TestRepositoryWithinTransaction' -count=1`

Expected: PASS.

- [ ] **Step 12: Commit transaction and advisory cleanup**

```bash
git add internal/repo/postgres.go internal/repo/sqlite.go internal/repo/postgres_transaction_test.go internal/repo/sqlite_test.go
git commit -m "fix: guarantee database transaction cleanup"
```

### Task 6: Derive and Validate the Internal OCI URL

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

**Interfaces:**
- Consumes: `Config.OCIListenAddress`, `OCIInternalURL`, `OCITLSCertFile`, and `OCITLSKeyFile`.
- Produces: `deriveOCIInternalURL(string, bool) (string, error)` and `validateOCIInternalURL(Config) error`.

- [ ] **Step 1: Update defaults and add failing derivation tests**

Change the expected no-cert default in both config snapshots from `https://127.0.0.1:5000` to `http://127.0.0.1:5000`.

Add tests asserting:

```go
url, err := deriveOCIInternalURL(":6000", false)
require.NoError(t, err)
assert.Equal(t, "http://127.0.0.1:6000", url)

url, err = deriveOCIInternalURL("0.0.0.0:7443", true)
require.NoError(t, err)
assert.Equal(t, "https://127.0.0.1:7443", url)
```

Add table cases for invalid scheme, missing host, HTTPS loopback without certs, HTTP loopback with certs, and explicit HTTPS external proxy without local certs.

- [ ] **Step 2: Run config tests and observe the wrong default/missing helpers**

Run: `go test ./internal/config -run 'TestLoadDefaults|TestDeriveOCIInternalURL|TestValidateOCIInternalURL' -count=1`

Expected: FAIL because the current default is HTTPS and both helper functions are undefined.

- [ ] **Step 3: Derive absent URLs and validate explicit URLs**

In `Load`, read OCI listen/cert/key environment values before constructing `Config`. Derive the fallback URL from the listener port and whether both TLS files are present, then pass that value to `envFallback("CHARM_REGISTRY_OCI_INTERNAL_URL", "APP_OCI_INTERNAL_URL", derived)`.

`deriveOCIInternalURL` uses `net.SplitHostPort`, always substitutes `127.0.0.1` for the host, and chooses `http` or `https` from the boolean.

`validateOCIInternalURL` parses with `url.Parse`, accepts only `http` and `https`, and requires a host. Resolve an omitted URL port to 80 for HTTP or 443 for HTTPS; resolve the embedded listener with `net.SplitHostPort`. Detect `localhost` or an IP whose `IsLoopback()` is true. When that loopback effective port equals the embedded listener port, require HTTPS exactly when both TLS files are set. Explicit external HTTP(S) URLs, including the existing `https://oci-internal.example.com` no-port case, remain accepted. Call this validator from `validateOCIConfig` after cert/key pairing.

- [ ] **Step 4: Run the complete config package**

Run: `go test ./internal/config -count=1`

Expected: PASS, including updated default snapshots and existing explicit URL fallbacks.

- [ ] **Step 5: Commit OCI self-connectivity validation**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "fix: align internal OCI URL with listener TLS"
```

### Task 7: Coordinate Server Startup, Failure, and Graceful Shutdown

**Files:**
- Modify: `cmd/charm-registry/main.go`
- Modify: `cmd/charm-registry/main_test.go`

**Interfaces:**
- Consumes: `app.App.Handler`, `app.App.OCIHandler`, `app.App.Close`, server constructors, and `Config.ServerShutdownTimeout`.
- Produces: `run`, `serverProcess`, and `coordinateServers` with deterministic close/error behavior.

- [ ] **Step 1: Add failing listener-coordination tests**

Define test server processes whose `serve` blocks on a channel and whose `shutdown` closes it. Add:

- `TestCoordinateServersSignalShutdownIsSuccessful`: cancel the root context, assert every shutdown called once, both serve functions returned, and result is nil when they return `http.ErrServerClosed`.
- `TestCoordinateServersUnexpectedFailureStopsSibling`: make API return `assert.AnError`, assert OCI shutdown is called, and result contains `assert.AnError`.
- `TestRunClosesApplicationBeforeReturning`: pass an already-canceled context and a close callback that records invocation; assert it ran exactly once before `run` returns.

Use one-second test deadlines so a goroutine leak fails instead of hanging the package.

- [ ] **Step 2: Run command tests and verify lifecycle symbols are absent**

Run: `go test ./cmd/charm-registry -run 'TestCoordinateServers|TestRunClosesApplication' -count=1`

Expected: FAIL to compile because `serverProcess`, `coordinateServers`, and the new `run` signature do not exist.

- [ ] **Step 3: Implement the error-returning runtime**

Add:

```go
type serverProcess struct {
	name     string
	serve    func() error
	shutdown func(context.Context) error
}
```

`coordinateServers` starts every `serve` in a buffered result channel. It waits for root cancellation or the first result. If a result arrives while `ctx.Err() == nil`, any early stop—including nil or `http.ErrServerClosed`—is recorded as unexpected; a real listener error is preserved. It then creates a detached shutdown context with the configured timeout, shuts down every process, waits for every serve result, ignores `http.ErrServerClosed` only from cancellation/shutdown, and joins unexpected listener/shutdown errors with the server name.

Implement:

```go
func run(
	ctx context.Context,
	cfg config.Config,
	apiHandler http.Handler,
	ociHandler http.Handler,
	closeApplication func() error,
) (err error)
```

Install a defer that joins `closeApplication()` into the named error. Build API and optional OCI `serverProcess` values using existing TLS selection, then call `coordinateServers`.

Change `serveOCI` to return an error rather than logging and calling `stop`. Move signal context construction into `realMain() int`; load config/build the app there, call `run`, log an error and return 1 on failure, or return 0. Keep `main` as only `os.Exit(realMain())`.

- [ ] **Step 4: Run all server command tests**

Run: `go test ./cmd/charm-registry -count=1`

Expected: PASS. Existing timeout-construction tests must remain unchanged.

- [ ] **Step 5: Commit deterministic server lifecycle**

```bash
git add cmd/charm-registry/main.go cmd/charm-registry/main_test.go
git commit -m "fix: coordinate registry server shutdown"
```

### Task 8: Add CLI Signals, Request Timeout, and Whole-Wait Deadline

**Files:**
- Modify: `cmd/charm-registryctl/main.go`
- Modify: `cmd/charm-registryctl/main_test.go`

**Interfaces:**
- Consumes: existing `run`, `doJSONStatus`, sync polling commands, and standard `http.Client`/signal contexts.
- Produces: `cliConfig.Client`, `--request-timeout`, `CHARM_REGISTRYCTL_REQUEST_TIMEOUT`, and a deadline-propagating `sync wait`.

- [ ] **Step 1: Add a failing per-request timeout test**

Start an `httptest.Server` whose handler blocks until `r.Context().Done()` and records cancellation. Run `sync list` with `--request-timeout 30ms`. Assert the command returns within one second, its error contains `Client.Timeout exceeded`, and the handler context was canceled.

- [ ] **Step 2: Run the request-timeout test and observe the hang risk**

Run: `go test ./cmd/charm-registryctl -run TestRunRequestTimeoutCancelsHTTP -count=1 -timeout=3s`

Expected: FAIL because `--request-timeout` is unknown and requests still use `http.DefaultClient`.

- [ ] **Step 3: Add the dedicated configurable client**

Extend `cliConfig` with `Client *http.Client`. Add `defaultCLIRequestTimeout = 30 * time.Second`. Parse `CHARM_REGISTRYCTL_REQUEST_TIMEOUT` before flags with `time.ParseDuration`, reject nonpositive values, and expose `--request-timeout`. After parsing, set `cfg.Client = &http.Client{Timeout: requestTimeout}`. Change `doJSONStatus` to call `cfg.Client.Do(req)` and return an explicit configuration error if `Client` is nil; every `run` path constructs it.

- [ ] **Step 4: Run request and existing CLI tests**

Run: `go test ./cmd/charm-registryctl -run 'TestRunRequestTimeoutCancelsHTTP|TestRunSyncList|TestRunSyncAdd|TestRunUnregister' -count=1`

Expected: PASS.

- [ ] **Step 5: Add failing whole-wait and parent-cancellation tests**

Add `TestRunSyncWaitTimeoutCancelsActivePoll`: the server blocks in the list handler, CLI request timeout is five seconds, whole wait timeout is 40ms, and the test asserts the handler context is canceled around 40ms and the returned error contains `timed out waiting for sync rules`.

Add `TestRunParentContextCancelsActiveRequest`: start `run` with a cancelable parent context and five-second request timeout, wait until the request starts, cancel the parent, and assert `errors.Is(err, context.Canceled)` plus server-side request cancellation.

- [ ] **Step 6: Run deadline tests and observe the in-flight request survives**

Run: `go test ./cmd/charm-registryctl -run 'TestRunSyncWaitTimeoutCancelsActivePoll|TestRunParentContextCancelsActiveRequest' -count=1 -timeout=3s`

Expected: the parent cancellation case passes through `NewRequestWithContext`, but the whole-wait case FAILS because its manual deadline does not cancel the active poll.

- [ ] **Step 7: Use one context deadline for the entire wait and a signal root in main**

At the start of `runSyncWait`, reject nonpositive timeout/interval, call `context.WithTimeout(ctx, timeout)`, and pass `waitCtx` to every list and retry request. Keep `lastPending` outside the loop. If `pendingSyncRules` returns while `waitCtx.Err()` is `context.DeadlineExceeded`, return `timed out waiting for sync rules` when `lastPending` is empty or `"timed out waiting for sync rules: " + strings.Join(lastPending, ", ")` otherwise; this is what converts expiry during the first active poll into the wait-specific error. Propagate parent cancellation unchanged. Replace `time.After` with one reset-free `time.NewTicker`, and apply the same timeout formatting in the ticker select's `waitCtx.Done()` branch.

Move executable entry handling to `realMain(args, stdout, stderr) int`, create its root with `signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)`, defer `stop`, call `run`, and let `main` call only `os.Exit(realMain(os.Args[1:], os.Stdout, os.Stderr))`.

- [ ] **Step 8: Run all CLI tests**

Run: `go test ./cmd/charm-registryctl -count=1`

Expected: PASS with no test taking longer than one second except existing polling coverage.

- [ ] **Step 9: Commit CLI cancellation and deadlines**

```bash
git add cmd/charm-registryctl/main.go cmd/charm-registryctl/main_test.go
git commit -m "fix: bound registryctl requests and waits"
```

### Task 9: Block HTTPS Redirect Downgrades

**Files:**
- Modify: `internal/charmhub/client.go`
- Modify: `internal/charmhub/client_test.go`

**Interfaces:**
- Consumes: `Client.checkRedirect(*http.Request, []*http.Request) error` and existing host/private-address/hop policies.
- Produces: an additional scheme invariant without changing host or hop behavior.

- [ ] **Step 1: Add failing direct redirect-policy tests**

Add table-driven `TestCheckRedirectRejectsHTTPSDowngrade` with requests constructed by `httptest.NewRequest`:

```go
tests := []struct {
	name    string
	start   string
	target  string
	wantErr string
}{
	{name: "same-host downgrade", start: "https://api.charmhub.io/start", target: "http://api.charmhub.io/artifact", wantErr: "https redirect downgrade blocked"},
	{name: "cdn downgrade", start: "https://api.charmhub.io/start", target: "http://canonical-bos01.cdn.snapcraftcontent.com/artifact", wantErr: "https redirect downgrade blocked"},
	{name: "https stays https", start: "https://api.charmhub.io/start", target: "https://api.charmhub.io/artifact"},
	{name: "development http stays http", start: "http://registry.test/start", target: "http://registry.test/artifact"},
}
```

Use a client whose base URL matches the start host. Assert the two downgrade cases return the exact marker and the compatible cases return nil.

- [ ] **Step 2: Run the redirect test and verify same-host HTTP is currently allowed**

Run: `go test ./internal/charmhub -run TestCheckRedirectRejectsHTTPSDowngrade -count=1`

Expected: FAIL because `isAllowedDownloadHost` currently accepts the host without checking the scheme.

- [ ] **Step 3: Add the scheme check before host validation**

After the hop-limit check, add:

```go
if len(via) > 0 &&
	strings.EqualFold(via[0].URL.Scheme, "https") &&
	!strings.EqualFold(req.URL.Scheme, "https") {
	return fmt.Errorf("https redirect downgrade blocked: %s", req.URL.String())
}
```

Keep the current host allowlist, private-address checks, and five-hop limit in their existing order after this check.

- [ ] **Step 4: Run redirect and race-focused Charmhub tests**

Run: `go test ./internal/charmhub -run 'TestCheckRedirectRejectsHTTPSDowngrade|TestAllowedDownloadHostAcceptsCharmhubCDNSubdomains|TestConcurrentDownloadToIsRaceFree' -count=1`

Expected: PASS.

Run: `go test -race ./internal/charmhub -run 'TestCheckRedirectRejectsHTTPSDowngrade|TestConcurrentDownloadToIsRaceFree' -count=1`

Expected: PASS with no race report.

- [ ] **Step 5: Commit redirect protection**

```bash
git add internal/charmhub/client.go internal/charmhub/client_test.go
git commit -m "fix: block Charmhub redirect downgrades"
```

### Task 10: Verify the Complete Operations Slice

**Files:**
- Verify only: all files changed in Tasks 1-9

**Interfaces:**
- Consumes: every interface produced by Tasks 1-9.
- Produces: formatted, generated-code-clean, buildable, vetted, race-checked unit-test evidence.

- [ ] **Step 1: Format the touched Go files**

Run: `make fmt`

Expected: PASS. Review `git diff --stat` and confirm formatting did not touch unrelated files.

- [ ] **Step 2: Verify generated SQL is current**

Run: `go tool sqlc diff`

Expected: PASS with no diff reported.

- [ ] **Step 3: Run focused packages without cache**

Run: `go test ./internal/repo ./internal/service ./internal/api ./internal/config ./internal/charmhub ./cmd/charm-registry ./cmd/charm-registryctl -count=1`

Expected: PASS.

- [ ] **Step 4: Run the repository and Charmhub race checks**

Run: `go test -race ./internal/repo ./internal/charmhub ./cmd/charm-registry ./cmd/charm-registryctl -count=1`

Expected: PASS with no race report.

- [ ] **Step 5: Run the repository-wide unit suite**

Run: `make test`

Expected: PASS for every package in `./internal/...` except generated `internal/repo/db`.

- [ ] **Step 6: Run the repository-wide internal race suite**

Run: `make test-race`

Expected: PASS for every package in `./internal/...` except generated `internal/repo/db`, with no race report.

- [ ] **Step 7: Build and vet production commands/packages**

Run: `make build`

Expected: PASS and produce `.bin/charm-registry` plus `.bin/charm-registryctl`.

Run: `make vet`

Expected: PASS with no diagnostics.

- [ ] **Step 8: Check whitespace and review scope**

Run: `git diff --check`

Expected: PASS with no whitespace errors.

Run: `git status --short`

Expected: only intentional slice-4 files are modified; no integration-test file appears.

- [ ] **Step 9: Commit any formatter-only adjustments after reviewing them**

```bash
git add \
  internal/repo/interface.go \
  internal/repo/memory.go internal/repo/memory_test.go \
  internal/repo/sqlite.go internal/repo/sqlite_search.go internal/repo/sqlite_test.go \
  internal/repo/postgres.go internal/repo/postgres_packages.go \
  internal/repo/postgres_sqlc.go internal/repo/postgres_scan_test.go \
  internal/repo/postgres_transaction_test.go internal/repo/queries/packages.sql \
  internal/repo/db/packages.sql.go internal/repo/db/querier.go \
  internal/service/authorization.go internal/service/packages.go internal/service/service_test.go \
  internal/api/http_releases.go internal/api/http_test.go \
  internal/config/config.go internal/config/config_test.go \
  internal/charmhub/client.go internal/charmhub/client_test.go \
  cmd/charm-registry/main.go cmd/charm-registry/main_test.go \
  cmd/charm-registryctl/main.go cmd/charm-registryctl/main_test.go
git commit -m "chore: verify operations hardening"
```

If `git status --short` is already clean, do not create an empty commit.
