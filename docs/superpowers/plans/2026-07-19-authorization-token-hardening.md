# Authorization and Token Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Centralize package authorization, prevent store-token escalation, and block cross-project OCI blob mounts while keeping compatibility umbrella permissions.

**Architecture:** Repositories return typed ACL facts and the service owns all access decisions. Token minting is separated from public issuance so exchange can only preserve or reduce authority. The OCI boundary rewrites unsafe mount attempts into normal uploads before Distribution sees them.

**Tech Stack:** Go 1.26.0 with toolchain 1.26.4, PostgreSQL/pgx/sqlc, SQLite/modernc, `net/http`, CNCF Distribution, Testify.

## Global Constraints

- Preserve existing public JSON response shapes.
- Keep anonymous public access only where the endpoint already permits it.
- `package-view` remains an umbrella for every `package-view-*` permission.
- `package-manage` remains an umbrella for every package permission.
- A domain management permission grants its matching domain view permission.
- A presented store token remains restricted even for public packages and administrator accounts.
- A channel-scoped token receives only matching channel rows; a direct channel lookup outside its scope is forbidden, and a no-channel composite lookup applies the existing default-stable/newest fallback selection within the allowed release set.
- Store tokens receive `403 token-delegation-forbidden` from `POST /v1/tokens`.
- Exchange copies store-token scopes exactly and never extends `ValidUntil`.
- Cross-project OCI mount attempts fall back to normal upload initiation.
- Do not hand-edit files under `internal/repo/db`; run `go tool sqlc generate`.
- Write a failing unit test before each production change.
- Do not run integration suites unless unit/backend coverage cannot prove an invariant.

---

## File structure

- Create `internal/core/authorization.go`: typed package roles and safe persisted-role parsing.
- Create `internal/service/authorization.go`: the single package/token policy evaluator and named endpoint policies.
- Modify `internal/repo/interface.go`: replace decision-shaped repository methods with `GetPackageRole`.
- Modify `internal/repo/queries/packages.sql`: return the strongest ACL fact for PostgreSQL.
- Regenerate `internal/repo/db/packages.sql.go` and `internal/repo/db/querier.go` from SQL.
- Modify `internal/repo/postgres_packages.go`, `internal/repo/sqlite.go`, and `internal/repo/memory.go`: implement identical role-fact semantics.
- Modify `internal/service/helpers.go`, `packages.go`, `revisions.go`, `resources.go`, and `releases.go`: route every affected endpoint through explicit named policies.
- Modify `internal/service/service.go` and `tokens.go`: permission implications, validated issuance, and authority-preserving exchange.
- Modify `internal/oci/client.go`: sanitize unsafe blob-mount query parameters.
- Test in `internal/core/constructors_test.go`, `internal/repo/{memory,sqlite,postgres_packages}_test.go`, `internal/service/service_test.go`, `internal/api/http_test.go`, and `internal/oci/client_test.go`.

### Task 1: Typed package-role repository facts

**Files:**
- Create: `internal/core/authorization.go`
- Modify: `internal/repo/interface.go`
- Modify: `internal/repo/queries/packages.sql`
- Modify: `internal/repo/postgres_packages.go`
- Modify: `internal/repo/sqlite.go`
- Modify: `internal/repo/memory.go`
- Regenerate: `internal/repo/db/packages.sql.go`
- Regenerate: `internal/repo/db/querier.go`
- Test: `internal/repo/memory_test.go`
- Test: `internal/repo/sqlite_test.go`
- Test: `internal/repo/postgres_test.go`
- Test: `internal/repo/postgres_packages_test.go`

**Interfaces:**
- Consumes: `core.Package.OwnerAccountID` remains the authoritative package owner fact.
- Produces: `core.PackageRole`, `core.ParsePackageRole(string) PackageRole`, `PackageRepo.GetPackageRole(context.Context, string, string) (core.PackageRole, error)`.

- [ ] **Step 1: Add failing cross-backend role tests**

Add table-driven assertions proving public visibility is not encoded as a role, role ordering is canonical, and unknown roles grant nothing:

```go
func TestMemoryGetPackageRoleReturnsACLFactOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository, owner := memWithAccount(t)
	require.NoError(t, repository.CreatePackage(ctx, core.Package{
		ID: "pkg-1", Name: "demo", Type: "charm", Private: false,
		Status: "registered", OwnerAccountID: owner.ID,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}))

	role, err := repository.GetPackageRole(ctx, "pkg-1", "stranger")
	require.NoError(t, err)
	assert.Equal(t, core.PackageRoleNone, role)

	repository.AddACLEntry("pkg-1", "account", "editor", "editor")
	role, err = repository.GetPackageRole(ctx, "pkg-1", "editor")
	require.NoError(t, err)
	assert.Equal(t, core.PackageRoleEditor, role)

	repository.AddACLEntry("pkg-1", "account", "unknown", "admin")
	role, err = repository.GetPackageRole(ctx, "pkg-1", "unknown")
	require.NoError(t, err)
	assert.Equal(t, core.PackageRoleNone, role)
}
```

Add a memory strongest-role case with a direct viewer ACL and a group editor ACL for the same account. Register the account in that group with the test-only helper described in Step 5 and assert `PackageRoleEditor`. Also add a group containing an unknown `admin` ACL and assert it cannot outrank a known direct role.

Add the SQLite persistence test:

```go
func TestSQLiteGetPackageRoleReturnsStrongestKnownACLFact(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := newSQLiteTestRepository(t)
	owner := ensureSQLiteAccount(t, repository, "owner", "owner")
	now := time.Now().UTC()
	require.NoError(t, repository.CreatePackage(ctx, core.Package{
		ID: "pkg-1", Name: "demo", Type: "charm", Private: false,
		Status: "registered", OwnerAccountID: owner.ID, CreatedAt: now, UpdatedAt: now,
	}))
	tests := []struct {
		accountID string
		role      string
		want      core.PackageRole
	}{
		{accountID: "viewer", role: "viewer", want: core.PackageRoleViewer},
		{accountID: "editor", role: "editor", want: core.PackageRoleEditor},
		{accountID: "owner-acl", role: "owner", want: core.PackageRoleOwner},
		{accountID: "invalid", role: "admin", want: core.PackageRoleNone},
	}
	for _, tt := range tests {
		ensureSQLiteAccount(t, repository, tt.accountID, tt.accountID)
		_, err := repository.db.ExecContext(ctx, `
INSERT INTO package_acl (package_id, principal_type, principal_id, role)
VALUES (?, 'account', ?, ?)`, "pkg-1", tt.accountID, tt.role)
		require.NoError(t, err)
		role, err := repository.GetPackageRole(ctx, "pkg-1", tt.accountID)
		require.NoError(t, err)
		assert.Equal(t, tt.want, role)
	}
	role, err := repository.GetPackageRole(ctx, "pkg-1", "stranger")
	require.NoError(t, err)
	assert.Equal(t, core.PackageRoleNone, role)
}
```

Extend the SQLite fixture with one account that has a direct viewer ACL plus group membership in an editor ACL, and assert `PackageRoleEditor`. Migrate the existing group-ACL behavior tests in `internal/repo/postgres_test.go` to the same role assertions so the strongest direct-or-group invariant remains covered after decision-shaped methods are removed.

In `internal/repo/postgres_packages_test.go`, extend the mock with a scan row and verify parsing:

```go
type postgresRowFunc func(...any) error

func (f postgresRowFunc) Scan(dest ...any) error { return f(dest...) }

type mockPostgresDB struct {
	execErr  error
	queryRow pgx.Row
}

func (m mockPostgresDB) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	return m.queryRow
}

func TestPostgresGetPackageRoleRejectsUnknownPersistedRole(t *testing.T) {
	t.Parallel()
	row := postgresRowFunc(func(dest ...any) error {
		*dest[0].(*string) = "admin"
		return nil
	})
	repository := &Postgres{db: mockPostgresDB{queryRow: row}}
	role, err := repository.GetPackageRole(context.Background(), "pkg-1", "acc-1")
	require.NoError(t, err)
	assert.Equal(t, core.PackageRoleNone, role)
}
```

- [ ] **Step 2: Run the focused tests and confirm the interface is missing**

Run:

```bash
go test ./internal/repo -run 'Test(Memory|SQLite|Postgres)GetPackageRole' -count=1
```

Expected: FAIL to compile because `GetPackageRole`, `core.PackageRoleNone`, and related symbols do not exist.

- [ ] **Step 3: Add the typed role and repository contract**

Create `internal/core/authorization.go`:

```go
package core

// PackageRole is an authorization fact returned by repository ACL lookup.
type PackageRole uint8

const (
	PackageRoleNone PackageRole = iota
	PackageRoleViewer
	PackageRoleEditor
	PackageRoleOwner
)

func ParsePackageRole(value string) PackageRole {
	switch value {
	case "viewer":
		return PackageRoleViewer
	case "editor":
		return PackageRoleEditor
	case "owner":
		return PackageRoleOwner
	default:
		return PackageRoleNone
	}
}

func (r PackageRole) AtLeast(required PackageRole) bool {
	return r >= required
}
```

Add the role-fact method alongside the two existing decision-shaped methods during this task so the repository and service remain buildable between commits:

```go
GetPackageRole(ctx context.Context, packageID, accountID string) (core.PackageRole, error)
```

- [ ] **Step 4: Replace PostgreSQL ACL decision queries with one fact query**

Add this query to `internal/repo/queries/packages.sql`; retain `GetPackageOwner`, `CanViewPackage`, and `CanManagePackage` until Task 2 moves every service caller:

```sql
-- name: GetPackageRole :one
SELECT COALESCE((
    SELECT acl.role
    FROM package_acl acl
    LEFT JOIN account_group_members gm
      ON acl.principal_type = 'group' AND acl.principal_id = gm.group_id
    WHERE acl.package_id = p.id
      AND ((acl.principal_type = 'account' AND acl.principal_id = sqlc.arg(account_id))
           OR gm.account_id = sqlc.arg(account_id))
    ORDER BY CASE acl.role
        WHEN 'owner' THEN 3
        WHEN 'editor' THEN 2
        WHEN 'viewer' THEN 1
        ELSE 0
    END DESC
    LIMIT 1
), '')::text AS role
FROM packages p
WHERE p.id = sqlc.arg(package_id);
```

Run:

```bash
go tool sqlc generate
go tool sqlc diff
```

Expected: generation succeeds and `sqlc diff` exits 0.

- [ ] **Step 5: Implement PostgreSQL, SQLite, and memory role lookup**

Use the generated PostgreSQL query and convert only known values:

```go
func (p *Postgres) GetPackageRole(ctx context.Context, packageID, accountID string) (core.PackageRole, error) {
	role, err := p.queries().GetPackageRole(ctx, sqlcdb.GetPackageRoleParams{
		PackageID: packageID,
		AccountID: accountID,
	})
	if pgxNotFound(err) {
		return core.PackageRoleNone, ErrNotFound
	}
	if err != nil {
		return core.PackageRoleNone, err
	}
	return core.ParsePackageRole(role), nil
}
```

Use the equivalent ordered correlated query in SQLite and retain its existing `ErrNotFound` translation:

```go
func (s *SQLite) GetPackageRole(ctx context.Context, packageID, accountID string) (core.PackageRole, error) {
	var role string
	err := s.db.QueryRowContext(ctx, `
SELECT COALESCE((
    SELECT acl.role
    FROM package_acl acl
    LEFT JOIN account_group_members gm
      ON acl.principal_type = 'group' AND acl.principal_id = gm.group_id
    WHERE acl.package_id = p.id
      AND ((acl.principal_type = 'account' AND acl.principal_id = ?) OR gm.account_id = ?)
    ORDER BY CASE acl.role
      WHEN 'owner' THEN 3 WHEN 'editor' THEN 2 WHEN 'viewer' THEN 1 ELSE 0 END DESC
    LIMIT 1
), '')
FROM packages p WHERE p.id = ?`, accountID, accountID, packageID).Scan(&role)
	if sqlNotFound(err) {
		return core.PackageRoleNone, ErrNotFound
	}
	if err != nil {
		return core.PackageRoleNone, err
	}
	return core.ParsePackageRole(role), nil
}
```

Memory checks package existence, scans direct and group ACL entries, and keeps the strongest parsed role. It does not special-case public packages or ownership. Add `groupMembers map[string]map[string]struct{}` to `Memory`, initialize it in `NewMemory`, and add a test-only `AddGroupMember(groupID, accountID string)` helper beside `AddACLEntry`:

```go
func (m *Memory) GetPackageRole(_ context.Context, packageID, accountID string) (core.PackageRole, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.packagesByID[packageID]; !ok {
		return core.PackageRoleNone, ErrNotFound
	}
	role := core.PackageRoleNone
	for _, entry := range m.acl[packageID] {
		direct := entry.PrincipalType == "account" && entry.PrincipalID == accountID
		_, inGroup := m.groupMembers[entry.PrincipalID][accountID]
		group := entry.PrincipalType == "group" && inGroup
		if direct || group {
			candidate := core.ParsePackageRole(entry.Role)
			if candidate > role {
				role = candidate
			}
		}
	}
	return role, nil
}
```

`AddGroupMember` takes `mu`, lazily creates the group member set, and inserts the account ID. This keeps the in-memory backend's strongest direct-or-group contract aligned with PostgreSQL and SQLite without adding group administration to the public repository interface.

Retain `canAccess`, `hasACLRole`, `CanViewPackage`, and `CanManagePackage` for the transitional Task 1 commit. Task 2 removes them after the centralized service policy has no old callers.

- [ ] **Step 6: Run repository tests and generated-code verification**

Run:

```bash
go test ./internal/repo -run 'Test(Memory|SQLite|Postgres)GetPackageRole' -count=1
go test ./internal/repo -count=1
go tool sqlc diff
```

Expected: PASS, PASS, and exit 0.

- [ ] **Step 7: Commit the role-fact boundary**

```bash
git add internal/core/authorization.go internal/repo/interface.go internal/repo/queries/packages.sql internal/repo/db/packages.sql.go internal/repo/db/querier.go internal/repo/postgres_packages.go internal/repo/sqlite.go internal/repo/memory.go internal/repo/memory_test.go internal/repo/sqlite_test.go internal/repo/postgres_test.go internal/repo/postgres_packages_test.go
git commit -m "fix: centralize package role facts"
```

### Task 2: Central service authorization policy and fine-grained reads

**Files:**
- Create: `internal/service/authorization.go`
- Modify: `internal/service/helpers.go`
- Modify: `internal/service/packages.go`
- Modify: `internal/service/revisions.go`
- Modify: `internal/service/resources.go`
- Modify: `internal/service/releases.go`
- Modify: `internal/api/http_packages.go`
- Modify: `internal/repo/interface.go`
- Modify: `internal/repo/queries/packages.sql`
- Modify: `internal/repo/postgres_packages.go`
- Modify: `internal/repo/sqlite.go`
- Modify: `internal/repo/memory.go`
- Test: `internal/repo/memory_test.go`
- Test: `internal/repo/postgres_test.go`
- Regenerate: `internal/repo/db/packages.sql.go`
- Regenerate: `internal/repo/db/querier.go`
- Test: `internal/service/service_test.go`

**Interfaces:**
- Consumes: `PackageRepo.GetPackageRole`, `core.PackageRole`, existing permission constants and `tokenAllowsPackage`.
- Produces: `packagePolicy`, named read/manage policies, and `Service.authorizePackage(context.Context, core.Identity, core.Package, packagePolicy) error`.

- [ ] **Step 1: Add failing policy-matrix tests**

Add tests covering the original bypass and token precedence:

```go
func TestSQLitePublicPackageCannotBeManagedByStranger(t *testing.T) {
	ctx := context.Background()
	repository, err := repo.NewSQLite(ctx, t.TempDir()+"/registry.sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, repository.Close()) })
	require.NoError(t, repository.Migrate(ctx))
	svc := New(testConfig(), repository, blob.NewMemoryStore(), testutil.OCIRegistry{})
	owner, err := svc.ResolveIdentity(ctx, auth.Claims{
		Subject: "oidc|owner", Username: "owner", DisplayName: "Owner", Email: "owner@example.com",
	}, nil)
	require.NoError(t, err)
	stranger, err := svc.ResolveIdentity(ctx, auth.Claims{
		Subject: "oidc|stranger", Username: "stranger", DisplayName: "Stranger", Email: "stranger@example.com",
	}, nil)
	require.NoError(t, err)
	_, err = svc.RegisterPackage(ctx, owner, "public-charm", "charm", false)
	require.NoError(t, err)

	_, err = svc.UpdatePackage(ctx, stranger, "public-charm", MetadataPatch{Title: stringPtr("stolen")})
	assertServiceError(t, err, ErrorKindForbidden)
}

func TestScopedAdminTokenDoesNotBypassPackageScopeOnPublicPackage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newTestService()
	owner := newIdentity("owner", "owner")
	_, err := svc.RegisterPackage(ctx, owner, "public-charm", "charm", false)
	require.NoError(t, err)
	admin := newIdentity("admin", "admin")
	admin.Account.IsAdmin = true
	admin.Token = &core.StoreToken{
		Packages: []core.PackageSelector{{Name: "other-charm", Type: "charm"}},
		Permissions: []string{permPackageViewMetadata},
	}

	_, err = svc.GetPackage(ctx, admin, "public-charm", true)
	assertServiceError(t, err, ErrorKindForbidden)
}
```

Add this direct policy table proving the permission implications without unrelated release fixtures:

```go
func TestFineGrainedPackagePermissions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, _ := newTestService()
	pkg := core.Package{ID: "pkg-1", Name: "demo", Type: "charm", Private: false}
	tests := []struct {
		name        string
		permissions []string
		policy      packagePolicy
		wantErr     bool
	}{
		{name: "metadata cannot read releases", permissions: []string{permPackageViewMetadata}, policy: publicReleasePolicy, wantErr: true},
		{name: "release permission reads releases", permissions: []string{permPackageViewReleases}, policy: publicReleasePolicy},
		{name: "view umbrella reads composite", permissions: []string{permPackageView}, policy: publicCompositePolicy},
		{name: "revision manager reads revisions", permissions: []string{permPackageManageRevisions}, policy: publicRevisionPolicy},
		{name: "revision manager cannot read releases", permissions: []string{permPackageManageRevisions}, policy: publicReleasePolicy, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			identity := newIdentity("acc-1", "alice")
			identity.Token = &core.StoreToken{Permissions: tt.permissions}
			err := svc.authorizePackage(ctx, identity, pkg, tt.policy)
			if tt.wantErr {
				assertServiceError(t, err, ErrorKindForbidden)
				return
			}
			require.NoError(t, err)
		})
	}
}
```

Add `TestChannelScopedTokenReadsOnlyAllowedReleaseData` with `latest/stable` and `latest/edge` releases and a token restricted to `latest/edge`. Assert `ListReleases` and `GetPackageInfo` contain only edge channel rows and that the info default release is edge. Add `TestChannelScopedTokenRejectsOtherInfoAndRefresh`: `GetPackageInfoForChannel(..., "latest/stable")` and a stable refresh action both return forbidden, while edge succeeds. For explicit-revision refresh, assert a revision released to an allowed channel succeeds and an unreleased/out-of-scope revision is forbidden. Add `TestChannelScopedTokenSearchUsesAllowedRelease` and assert a package whose global default is stable is returned with edge as its find release.

- [ ] **Step 2: Run the focused service tests and confirm the old policy fails**

Run:

```bash
go test ./internal/service -run 'Test(SQLitePublicPackageCannotBeManagedByStranger|ScopedAdminTokenDoesNotBypassPackageScope|FineGrainedPackagePermissions|ChannelScopedToken)' -count=1
```

Expected: at least the public-package SQLite/memory semantic or scoped-admin case FAILS because the current guards short-circuit before token scope or use backend decisions.

- [ ] **Step 3: Define explicit named policies and permission implication**

Create `internal/service/authorization.go` with:

```go
package service

import (
	"context"
	"strings"

	"github.com/gschiano/charm-registry/internal/core"
)

type packagePolicy struct {
	minimumRole         core.PackageRole
	allowAnonymousPublic bool
	permissions         []string
	channel             string
}

var (
	publicMetadataPolicy = packagePolicy{allowAnonymousPublic: true, permissions: []string{permPackageViewMetadata}}
	publicRevisionPolicy = packagePolicy{allowAnonymousPublic: true, permissions: []string{permPackageViewRevisions}}
	publicReleasePolicy  = packagePolicy{allowAnonymousPublic: true, permissions: []string{permPackageViewReleases}}
	publicCompositePolicy = packagePolicy{allowAnonymousPublic: true, permissions: []string{
		permPackageViewMetadata, permPackageViewRevisions, permPackageViewReleases,
	}}
	managePackagePolicy  = packagePolicy{minimumRole: core.PackageRoleEditor, permissions: []string{permPackageManage}}
	manageMetadataPolicy = packagePolicy{minimumRole: core.PackageRoleEditor, permissions: []string{permPackageManageMetadata}}
	manageRevisionPolicy = packagePolicy{minimumRole: core.PackageRoleEditor, permissions: []string{permPackageManageRevisions}}
	manageReleasePolicy  = packagePolicy{minimumRole: core.PackageRoleEditor, permissions: []string{permPackageManageReleases}}
)

func permissionImplies(granted, required string) bool {
	if granted == required || granted == permPackageManage && strings.HasPrefix(required, "package-") {
		return true
	}
	if granted == permPackageView && strings.HasPrefix(required, "package-view-") {
		return true
	}
	return granted == permPackageManageMetadata && required == permPackageViewMetadata ||
		granted == permPackageManageRevisions && required == permPackageViewRevisions ||
		granted == permPackageManageReleases && required == permPackageViewReleases
}
```

- [ ] **Step 4: Implement the single policy evaluator**

Add the evaluator, keeping token restrictions ahead of administrator expansion:

```go
func (s *Service) authorizePackage(
	ctx context.Context,
	identity core.Identity,
	pkg core.Package,
	policy packagePolicy,
) error {
	if identity.System {
		return nil
	}
	if !identity.Authenticated {
		if policy.allowAnonymousPublic && !pkg.Private && policy.minimumRole == core.PackageRoleNone {
			return nil
		}
		return newError(ErrorKindUnauthorized, "unauthorized", "authentication required")
	}
	if identity.Token != nil {
		if len(identity.Token.Packages) > 0 && !tokenAllowsPackage(identity.Token, pkg) {
			return newError(ErrorKindForbidden, "forbidden", "token does not allow this package")
		}
		for _, required := range policy.permissions {
			allowed := false
			for _, granted := range identity.Token.Permissions {
				if permissionImplies(granted, required) {
					allowed = true
					break
				}
			}
			if !allowed {
				return newError(ErrorKindForbidden, "forbidden", "token does not grant required permission")
			}
		}
		if policy.channel != "" {
			if err := s.enforceChannelRestriction(identity, policy.channel); err != nil {
				return err
			}
		}
	}
	if identity.Account.IsAdmin {
		return nil
	}
	if policy.minimumRole == core.PackageRoleNone && !pkg.Private {
		return nil
	}
	role := core.PackageRoleNone
	if pkg.OwnerAccountID == identity.Account.ID {
		role = core.PackageRoleOwner
	} else {
		var err error
		role, err = s.repo.GetPackageRole(ctx, pkg.ID, identity.Account.ID)
		if err != nil {
			return err
		}
	}
	required := policy.minimumRole
	if required == core.PackageRoleNone {
		required = core.PackageRoleViewer
	}
	if !role.AtLeast(required) {
		return newError(ErrorKindForbidden, "forbidden", "package access is not allowed")
	}
	return nil
}
```

Make account-level permission checks token-first as well, so an administrator using a scoped token cannot bypass its permissions:

```go
func (s *Service) requirePermission(identity core.Identity, permission string) error {
	if identity.System {
		return nil
	}
	if err := s.requireAuth(identity); err != nil {
		return err
	}
	if identity.Token == nil {
		return nil
	}
	for _, granted := range identity.Token.Permissions {
		if permissionImplies(granted, permission) {
			return nil
		}
	}
	return newError(ErrorKindForbidden, "forbidden", "token does not grant required permission")
}
```

Keep `requireAuth`, `tokenAllowsPackage`, `enforceChannelRestriction`, and unrelated helpers in `helpers.go`. Delete the old `requirePermissionOrAnonymous`, `requirePackageView`, and backend-decision code after callers move.

Add these channel helpers beside the existing restriction helper:

```go
func tokenAllowsChannel(identity core.Identity, channel string) bool {
	if identity.Token == nil || len(identity.Token.Channels) == 0 {
		return true
	}
	for _, allowed := range identity.Token.Channels {
		if allowed == channel {
			return true
		}
	}
	return false
}

func packagePolicyForChannel(base packagePolicy, channel string) packagePolicy {
	base.channel = channel
	return base
}
```

Channel collection semantics are explicit: a token without channel restrictions sees the existing collection unchanged; a restricted token retains only exact allowed release channels before revision IDs or response rows are derived. A no-channel info/find lookup applies the existing default-track stable preference and newest-`When`/ascending-ID fallback to that filtered set; no existing allowed release is forbidden for direct info and omitted from search. Direct OIDC and anonymous callers retain `ResolveDefaultRelease`.

- [ ] **Step 5: Route endpoints through explicit policies**

Replace guard calls using this exact mapping:

```go
// Package metadata/listing.
s.authorizePackage(ctx, identity, pkg, publicMetadataPolicy)

// Revision/resource lists and downloads.
s.authorizePackage(ctx, identity, pkg, publicRevisionPolicy)

// Release list/channel data.
s.authorizePackage(ctx, identity, pkg, publicReleasePolicy)

// Find/info/refresh responses that expose all three domains.
s.authorizePackage(ctx, identity, pkg, publicCompositePolicy)

// Mutations.
s.authorizePackage(ctx, identity, pkg, managePackagePolicy)
s.authorizePackage(ctx, identity, pkg, manageMetadataPolicy)
s.authorizePackage(ctx, identity, pkg, manageRevisionPolicy)
s.authorizePackage(ctx, identity, pkg, manageReleasePolicy)
```

Remove the boolean from package lookup by splitting the public metadata use case from an internal policy-aware helper:

```go
func (s *Service) getPackageWithPolicy(
	ctx context.Context,
	identity core.Identity,
	name string,
	policy packagePolicy,
) (core.Package, error) {
	pkg, err := s.repo.GetPackageByName(ctx, name)
	if err != nil {
		return core.Package{}, translateRepoError(err, messagePackageNotFound)
	}
	if err := s.authorizePackage(ctx, identity, pkg, policy); err != nil {
		return core.Package{}, err
	}
	return s.enrichPackage(ctx, pkg)
}

func (s *Service) GetPackage(ctx context.Context, identity core.Identity, name string) (core.Package, error) {
	return s.getPackageWithPolicy(ctx, identity, name, publicMetadataPolicy)
}
```

Update `internal/api/http_packages.go` to call the three-argument method. Make `info` call `getPackageWithPolicy` using `packagePolicyForChannel(publicCompositePolicy, channel)` when a channel was supplied. With no supplied channel, apply `publicCompositePolicy`, resolve the first existing token-allowed release as described above, and filter `ChannelMap` before loading its revisions. Use this exact remaining mapping:

```text
packages.go SearchPackages/canSeePackage       publicCompositePolicy
releases.go ListReleases                      publicReleasePolicy
releases.go ResolveRefresh action             publicCompositePolicy
resources.go ListResources/ListRevisions      publicRevisionPolicy
resources.go DownloadResourceStream           publicRevisionPolicy
revisions.go ReviewUpload/ListRevisions       publicRevisionPolicy
revisions.go DownloadCharmStream              publicRevisionPolicy
```

Change `packageFindResult` to accept `identity`, resolve its visible release as described above, and have `SearchPackages` skip packages with no token-allowed release. In `ResolveRefresh`, apply `publicCompositePolicy` before package data access, then enforce the normalized/effective channel before loading the selected revision or resources. For an explicit-revision action, keep the current selection precedence but list releases and require at least one row whose revision matches and whose channel is allowed; use that row only as the authorization channel and do not change the existing empty effective-channel response. `ListReleases` applies `publicReleasePolicy`, filters releases to allowed channels before `ListRevisionsByNumbers`, and derives its package channel descriptors from the filtered releases.

For release creation, copy `manageReleasePolicy`, set `channel` to the request channel, and authorize each validated item.

After `ListRegisteredPackages` loads candidates, filter them through `publicMetadataPolicy` before enrichment. This preserves the account-level `account-view-packages` gate while applying a presented admin token's package selectors:

```go
visible := packages[:0]
for _, pkg := range packages {
	err := s.authorizePackage(ctx, identity, pkg, publicMetadataPolicy)
	if err == nil {
		visible = append(visible, pkg)
		continue
	}
	var policyErr *Error
	if !errors.As(err, &policyErr) ||
		(policyErr.Kind != ErrorKindUnauthorized && policyErr.Kind != ErrorKindForbidden) {
		return nil, err
	}
}
return s.enrichPackages(ctx, visible)
```

Once `rg -n 'CanViewPackage|CanManagePackage' internal/service --glob '*.go'` returns no matches, remove both methods from `PackageRepo` and all three backends, remove `GetPackageOwner`, `CanViewPackage`, and `CanManagePackage` from `packages.sql`, then regenerate:

```bash
go tool sqlc generate
go tool sqlc diff
```

Expected: generation succeeds and `sqlc diff` exits 0.

Migrate the existing repository tests in `internal/repo/memory_test.go` and `internal/repo/postgres_test.go` from `CanViewPackage`/`CanManagePackage` assertions to exact `GetPackageRole` assertions before removing the old concrete methods. Migrate every four-argument `Service.GetPackage(ctx, identity, name, requireViewPermission)` call in `internal/service/service_test.go` to the three-argument API or the explicit `getPackageWithPolicy` helper when a composite-domain fixture needs it. Require both searches to be empty before the task commit:

```bash
rg -n '\.(CanViewPackage|CanManagePackage)\(' internal --glob '*.go' --glob '!internal/repo/db/**'
rg -n 'GetPackage\([^\n]+, *(true|false)\)' internal --glob '*.go'
```

- [ ] **Step 6: Run service and API authorization tests**

Run:

```bash
go test ./internal/service -run 'Test(SQLitePublicPackageCannotBeManagedByStranger|ScopedAdminTokenDoesNotBypassPackageScope|FineGrainedPackagePermissions|ChannelScopedToken|PrivatePackagesRequireAuthentication|AnonymousPublicConsumerServiceAccess|TokenPackageScoping)' -count=1
go test ./internal/api -run 'Test(AnonymousConsumerRoutesDoNotExposePrivatePackage|PackageMutationRouteAuthBoundaries|ProtectedRoutesRemainAuthenticated)' -count=1
```

Expected: PASS.

- [ ] **Step 7: Run all affected packages and commit**

```bash
go test ./internal/core ./internal/repo ./internal/service ./internal/api -count=1
git add internal/service/authorization.go internal/service/helpers.go internal/service/packages.go internal/service/revisions.go internal/service/resources.go internal/service/releases.go internal/service/service_test.go internal/api/http_packages.go internal/api/http_test.go internal/repo/interface.go internal/repo/queries/packages.sql internal/repo/db/packages.sql.go internal/repo/db/querier.go internal/repo/postgres_packages.go internal/repo/postgres_test.go internal/repo/sqlite.go internal/repo/memory.go internal/repo/memory_test.go
git commit -m "fix: enforce centralized package authorization"
```

Expected: tests PASS and commit succeeds.

### Task 3: Non-delegable issuance and authority-preserving token exchange

**Files:**
- Modify: `internal/service/service.go`
- Modify: `internal/service/tokens.go`
- Test: `internal/service/service_test.go`
- Test: `internal/api/http_test.go`

**Interfaces:**
- Consumes: the known permission constants and `core.NewStoreToken`.
- Produces: private `tokenMintRequest`, `Service.mintStoreToken`, validated direct issuance, and capped exchange.

- [ ] **Step 1: Add failing escalation and expiry tests**

Add:

```go
func TestIssueStoreTokenRejectsStoreTokenIdentity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, repository := newTestService()
	identity := newIdentity("acc-1", "alice")
	identity.Token = &core.StoreToken{
		AccountID: identity.Account.ID,
		Permissions: []string{permPackageViewMetadata},
		ValidUntil: time.Now().Add(time.Hour),
	}

	_, _, err := svc.IssueStoreToken(ctx, identity, IssueTokenRequest{})
	svcErr := serviceError(t, err)
	assert.Equal(t, ErrorKindForbidden, svcErr.Kind)
	assert.Equal(t, "token-delegation-forbidden", svcErr.Code)
	tokens, listErr := repository.ListStoreTokens(ctx, identity.Account.ID, true)
	require.NoError(t, listErr)
	assert.Empty(t, tokens)
}

func TestExchangeStoreTokenPreservesEmptyPermissionsAndCapsExpiry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, repository := newTestService()
	svc.Clock = func() time.Time { return time.Unix(1_000, 0).UTC() }
	identity := newIdentity("acc-1", "alice")
	parentExpiry := svc.now().Add(15 * time.Minute)
	identity.Token = &core.StoreToken{
		AccountID: identity.Account.ID,
		Permissions: []string{},
		Packages: []core.PackageSelector{{Name: "demo", Type: "charm"}},
		Channels: []string{"latest/stable"},
		ValidUntil: parentExpiry,
	}

	_, err := svc.ExchangeStoreToken(ctx, identity, nil)
	require.NoError(t, err)
	tokens, err := repository.ListStoreTokens(ctx, identity.Account.ID, true)
	require.NoError(t, err)
	require.Len(t, tokens, 1)
	assert.Empty(t, tokens[0].Permissions)
	assert.Equal(t, identity.Token.Packages, tokens[0].Packages)
	assert.Equal(t, identity.Token.Channels, tokens[0].Channels)
	assert.Equal(t, parentExpiry, tokens[0].ValidUntil)
}
```

Add direct-issuance validation cases:

```go
func TestIssueStoreTokenRejectsInvalidRestrictions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tests := []struct {
		name string
		req  IssueTokenRequest
	}{
		{name: "unknown permission", req: IssueTokenRequest{Permissions: []string{"package-superuser"}}},
		{name: "non-positive ttl", req: IssueTokenRequest{TTL: intPtr(0)}},
		{name: "selector with id and name", req: IssueTokenRequest{Packages: []core.PackageSelector{{ID: "pkg-1", Name: "demo", Type: "charm"}}}},
		{name: "selector without id or name", req: IssueTokenRequest{Packages: []core.PackageSelector{{Type: "charm"}}}},
		{name: "unsupported selector type", req: IssueTokenRequest{Packages: []core.PackageSelector{{Name: "demo", Type: "bundle"}}}},
		{name: "empty channel", req: IssueTokenRequest{Channels: []string{""}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newTestService()
			_, _, err := svc.IssueStoreToken(ctx, newIdentity("acc-1", "alice"), tt.req)
			assertServiceError(t, err, ErrorKindInvalidRequest)
		})
	}
}
```

Add `TestIssueStoreTokenPreservesExplicitEmptyPermissions`: issue with `Permissions: []string{}` and assert the persisted token has an empty permission slice. Keep the existing default-permissions test using an omitted (`nil`) field.

Add the HTTP boundary test:

```go
func TestStoreTokenCannotIssueTokenButCanExchange(t *testing.T) {
	t.Parallel()
	handler := newTestHandler(t, testCfg)
	response := doRequest(t, handler, http.MethodPost, "/v1/tokens/exchange", nil, "Bearer dev:alice:Alice")
	require.Equal(t, http.StatusOK, response.Code)
	token := decodeJSON(t, response)["macaroon"].(string)

	response = doRequest(t, handler, http.MethodPost, "/v1/tokens", map[string]any{}, "Bearer "+token)
	assert.Equal(t, http.StatusForbidden, response.Code)
	assert.Contains(t, response.Body.String(), "token-delegation-forbidden")

	response = doRequest(t, handler, http.MethodPost, "/v1/tokens/exchange", nil, "Bearer "+token)
	assert.Equal(t, http.StatusOK, response.Code)
}
```

- [ ] **Step 2: Run the focused tests and confirm escalation exists**

Run:

```bash
go test ./internal/service -run 'Test(IssueStoreTokenRejectsStoreTokenIdentity|IssueStoreTokenRejectsInvalidRestrictions|ExchangeStoreTokenPreservesEmptyPermissionsAndCapsExpiry)' -count=1
go test ./internal/api -run 'TestStoreTokenCannotIssueTokenButCanExchange' -count=1
```

Expected: FAIL because direct issuance accepts the token and exchange applies defaults/extends validity.

- [ ] **Step 3: Separate token minting from direct-issuance policy**

Add the private request and helper in `tokens.go`:

```go
type tokenMintRequest struct {
	description *string
	permissions []string
	packages    []core.PackageSelector
	channels    []string
	validUntil  time.Time
}

func (s *Service) mintStoreToken(
	ctx context.Context,
	accountID string,
	req tokenMintRequest,
) (string, core.StoreToken, error) {
	raw, hash, err := auth.NewOpaqueToken()
	if err != nil {
		return "", core.StoreToken{}, err
	}
	now := s.now()
	token, err := core.NewStoreToken(core.StoreToken{
		SessionID: uuid.NewString(), TokenHash: hash,
		TokenPrefix: auth.TokenPrefixFromRaw(raw), HashScheme: auth.TokenHashSchemeBcrypt,
		AccountID: accountID, Description: req.description,
		Packages: append([]core.PackageSelector(nil), req.packages...),
		Channels: append([]string(nil), req.channels...),
		Permissions: append([]string(nil), req.permissions...),
		ValidSince: now, ValidUntil: req.validUntil,
	})
	if err != nil {
		return "", core.StoreToken{}, err
	}
	if err := s.accounts.CreateStoreToken(ctx, token); err != nil {
		return "", core.StoreToken{}, err
	}
	return raw, token, nil
}
```

Keep audit logging in the public issuance/exchange caller after successful persistence, or factor a shared log helper that does not change policy.

- [ ] **Step 4: Validate direct issuance and reject delegation**

Add a known permission set next to `defaultPermissions` and validate requests:

```go
var knownPermissions = map[string]struct{}{
	permAccountRegisterPackage: {}, permAccountViewPackages: {},
	permPackageManage: {}, permPackageManageMetadata: {},
	permPackageManageReleases: {}, permPackageManageRevisions: {},
	permPackageView: {}, permPackageViewMetadata: {},
	permPackageViewReleases: {}, permPackageViewRevisions: {},
}
```

At the start of `IssueStoreToken`:

```go
if err := s.requireAuth(identity); err != nil {
	return "", core.StoreToken{}, err
}
if identity.Token != nil {
	return "", core.StoreToken{}, newError(
		ErrorKindForbidden,
		"token-delegation-forbidden",
		"store tokens cannot issue new tokens; use token exchange",
	)
}
permissions := append([]string(nil), req.Permissions...)
if req.Permissions == nil {
	permissions = append([]string(nil), defaultPermissions...)
}
for _, permission := range permissions {
	if _, ok := knownPermissions[permission]; !ok {
		return "", core.StoreToken{}, newError(ErrorKindInvalidRequest, "invalid-request", "unknown permission: "+permission)
	}
}
ttl := 30 * time.Hour
if req.TTL != nil {
	seconds := int64(*req.TTL)
	const maxTTLSeconds = int64(time.Duration(1<<63-1) / time.Second)
	if seconds <= 0 || seconds > maxTTLSeconds {
		return "", core.StoreToken{}, newError(ErrorKindInvalidRequest, "invalid-request", "ttl must be a positive duration")
	}
	ttl = time.Duration(seconds) * time.Second
}
return s.mintStoreToken(ctx, identity.Account.ID, tokenMintRequest{
	description: req.Description, permissions: permissions,
	packages: req.Packages, channels: req.Channels,
	validUntil: s.now().Add(ttl),
})
```

Validate selector and channel structure before minting:

```go
for _, selector := range req.Packages {
	if (selector.ID == "") == (selector.Name == "") {
		return "", core.StoreToken{}, newError(
			ErrorKindInvalidRequest, "invalid-request",
			"package selector must contain exactly one of id or name",
		)
	}
	if selector.Type != "" && selector.Type != "charm" {
		return "", core.StoreToken{}, newError(
			ErrorKindInvalidRequest, "invalid-request", "unsupported package selector type",
		)
	}
}
for _, channel := range req.Channels {
	if strings.TrimSpace(channel) == "" {
		return "", core.StoreToken{}, newError(
			ErrorKindInvalidRequest, "invalid-request", "channel restriction cannot be empty",
		)
	}
}
```

- [ ] **Step 5: Implement capped scope-preserving exchange**

Use direct issuance only for a direct identity. For a parent token, preserve slices exactly:

```go
func (s *Service) ExchangeStoreToken(ctx context.Context, identity core.Identity, description *string) (string, error) {
	if err := s.requireAuth(identity); err != nil {
		return "", err
	}
	if identity.Token == nil {
		raw, _, err := s.IssueStoreToken(ctx, identity, IssueTokenRequest{Description: description})
		return raw, err
	}
	now := s.now()
	validUntil := now.Add(30 * time.Hour)
	if identity.Token.ValidUntil.Before(validUntil) {
		validUntil = identity.Token.ValidUntil
	}
	if !validUntil.After(now) {
		return "", newError(ErrorKindUnauthorized, "unauthorized", "store token has expired")
	}
	raw, _, err := s.mintStoreToken(ctx, identity.Account.ID, tokenMintRequest{
		description: description,
		permissions: identity.Token.Permissions,
		packages: identity.Token.Packages,
		channels: identity.Token.Channels,
		validUntil: validUntil,
	})
	return raw, err
}
```

Update existing exchange fixtures so parent tokens include a future `ValidUntil`.

- [ ] **Step 6: Run token service and HTTP tests**

```bash
go test ./internal/service -run 'Test(IssueStoreToken|ExchangeStoreToken)' -count=1
go test ./internal/api -run 'Test(Issue|Exchange|StoreTokenCannotIssue)' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit token hardening**

```bash
git add internal/service/service.go internal/service/tokens.go internal/service/service_test.go internal/api/http_test.go
git commit -m "fix: prevent store token delegation"
```

### Task 4: Cross-project OCI mount fallback

**Files:**
- Modify: `internal/oci/client.go`
- Test: `internal/oci/client_test.go`

**Interfaces:**
- Consumes: existing `repositoryProject`, package-specific push authentication, and `http.Request.Clone`.
- Produces: `requestWithoutUnsafeMount(*http.Request, string) *http.Request` used immediately before forwarding to Distribution.

- [ ] **Step 1: Add failing mount-forwarding tests**

Extend `TestAuthMiddlewareEnforcesPackageScopedBasicAuth` with a downstream handler that records the forwarded query. Add separate subtests:

```go
func TestAuthMiddlewareStripsCrossProjectMount(t *testing.T) {
	memory := repo.NewMemory()
	client := testClient(memory)
	pkg, err := client.SyncPackage(context.Background(), core.Package{ID: "pkg-1", Name: "demo"})
	require.NoError(t, err)
	require.NoError(t, memory.CreatePackage(context.Background(), pkg))
	username, password, err := client.Credentials(pkg, false)
	require.NoError(t, err)

	var forwarded url.Values
	handler := client.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded = r.URL.Query()
		w.WriteHeader(http.StatusAccepted)
	}))
	req := httptest.NewRequest(http.MethodPost,
		"/v2/charm-demo/app/blobs/uploads/?mount=sha256:deadbeef&from=charm-private/app", nil)
	req.SetBasicAuth(username, password)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	assert.Equal(t, http.StatusAccepted, recorder.Code)
	assert.False(t, forwarded.Has("mount"))
	assert.False(t, forwarded.Has("from"))
}
```

Add exact parameter-shape cases:

```go
func TestRequestWithoutUnsafeMount(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		rawQuery  string
		wantMount bool
	}{
		{name: "no mount", rawQuery: "foo=bar"},
		{name: "same project", rawQuery: "mount=sha256%3Aabc&from=charm-demo%2Fother", wantMount: true},
		{name: "cross project", rawQuery: "mount=sha256%3Aabc&from=charm-private%2Fother"},
		{name: "mount only", rawQuery: "mount=sha256%3Aabc"},
		{name: "from only", rawQuery: "from=charm-demo%2Fother"},
		{name: "duplicate mount", rawQuery: "mount=one&mount=two&from=charm-demo%2Fother"},
		{name: "empty source", rawQuery: "mount=sha256%3Aabc&from="},
		{name: "source without repository", rawQuery: "mount=sha256%3Aabc&from=charm-demo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rawQuery := tt.rawQuery
			if rawQuery != "" {
				rawQuery += "&"
			}
			rawQuery += "foo=bar"
			req := httptest.NewRequest(http.MethodPost, "/v2/charm-demo/app/blobs/uploads/?"+rawQuery, nil)
			got := requestWithoutUnsafeMount(req, "charm-demo").URL.Query()
			assert.Equal(t, tt.wantMount, got.Has("mount"))
			assert.Equal(t, tt.wantMount, got.Has("from"))
			assert.Equal(t, "bar", got.Get("foo"))
		})
	}
}
```

- [ ] **Step 2: Run the mount test and confirm unsafe parameters are forwarded**

```bash
go test ./internal/oci -run 'Test(AuthMiddleware(StripsCrossProjectMount|PreservesSameProjectMount)|RequestWithoutUnsafeMount)' -count=1
```

Expected: FAIL because the downstream handler receives `mount` and `from` unchanged.

- [ ] **Step 3: Implement request cloning and mount sanitization**

Add:

```go
func requestWithoutUnsafeMount(r *http.Request, destinationProject string) *http.Request {
	query := r.URL.Query()
	mountValues, hasMount := query["mount"]
	fromValues, hasFrom := query["from"]
	if !hasMount && !hasFrom {
		return r
	}
	safe := len(mountValues) == 1 && mountValues[0] != "" &&
		len(fromValues) == 1
	if safe {
		source := fromValues[0]
		sourceProject, remainder, ok := strings.Cut(source, "/")
		safe = ok && sourceProject == destinationProject && validMountRepositoryRemainder(remainder)
	}
	if safe {
		return r
	}
	clone := r.Clone(r.Context())
	clonedURL := *r.URL
	query.Del("mount")
	query.Del("from")
	clonedURL.RawQuery = query.Encode()
	clone.URL = &clonedURL
	return clone
}
```

Use the strict helper below; do not trim or normalize attacker input into a valid source:

```go
func validMountRepositoryRemainder(value string) bool {
	if value == "" || strings.HasPrefix(value, "/") ||
		strings.HasSuffix(value, "/") || strings.Contains(value, "//") {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}
```

Add cases for `/charm-demo/app`, `charm-demo/app/`, `charm-demo//app`, `charm-demo/../app`, and `charm-demo/./app`; all must lose both parameters.

Sanitize only blob-upload initiation requests. Add:

```go
func isBlobUploadInitiation(r *http.Request) bool {
	return r.Method == http.MethodPost &&
		strings.HasSuffix(strings.TrimSuffix(r.URL.Path, "/"), "/blobs/uploads")
}
```

Immediately after destination push authorization succeeds in `authMiddleware`, call:

```go
if isBlobUploadInitiation(r) {
	r = requestWithoutUnsafeMount(r, pkg.OCIProject)
}
next.ServeHTTP(w, r)
```

- [ ] **Step 4: Run all OCI tests**

```bash
go test ./internal/oci -count=1
```

Expected: PASS, including existing pull/push credential behavior.

- [ ] **Step 5: Commit OCI mount protection**

```bash
git add internal/oci/client.go internal/oci/client_test.go
git commit -m "fix: block cross-project OCI blob mounts"
```

### Task 5: Slice verification and reviewer gate

**Files:**
- Modify only if verification exposes a defect in Tasks 1-4.

**Interfaces:**
- Consumes: all authorization-slice deliverables.
- Produces: a buildable, formatted slice ready for the artifact-lifecycle plan.

- [ ] **Step 1: Format touched Go files**

```bash
make fmt
```

Expected: exits 0.

- [ ] **Step 2: Run focused package tests and race checks**

```bash
go test ./internal/core ./internal/repo ./internal/service ./internal/api ./internal/oci -count=1
go test -race ./internal/service ./internal/oci -count=1
```

Expected: PASS.

- [ ] **Step 3: Verify generated code, static analysis, and binaries**

```bash
go tool sqlc diff
go vet ./cmd/... ./internal/...
make build
```

Expected: all commands exit 0.

- [ ] **Step 4: Inspect scope and dead code**

```bash
git status --short
git diff --stat HEAD~4..HEAD
rg -n 'CanViewPackage|CanManagePackage|canAccess|requirePermissionOrAnonymous' internal --glob '*.go'
```

Expected: only planned files are changed; the final search has no production references to removed authorization paths. If now-unused elements remain, list them for explicit removal approval rather than deleting unrelated code.

- [ ] **Step 5: Request the per-slice reviewer gate**

Provide the reviewer with the four task commits, this plan, and the design spec. Require separate confirmation of:

```text
1. PostgreSQL, SQLite, and memory role semantics match.
2. Token restrictions precede admin/public shortcuts.
3. Fine-grained permissions retain umbrella compatibility.
4. Exchange cannot increase expiry or scopes.
5. Cross-project mount parameters never reach Distribution.
```

Expected: reviewer approves or returns actionable findings before Task 1 of the artifact-lifecycle plan begins.
