package repo

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gschiano/charm-registry/internal/core"
)

func newRepositoryBehaviorTestRepository(t *testing.T) *SQLite {
	t.Helper()
	return newSQLiteTestRepository(t)
}

func ensureRepositoryBehaviorTestAccount(t *testing.T, repository *SQLite, id, username string) core.Account {
	t.Helper()
	return ensureSQLiteAccount(t, repository, id, username)
}

func createRepositoryBehaviorTestPackage(t *testing.T, repository *SQLite, owner core.Account, pkg core.Package) core.Package {
	t.Helper()

	now := time.Now().UTC()
	if pkg.ID == "" {
		pkg.ID = "pkg-1"
	}
	if pkg.Name == "" {
		pkg.Name = "demo"
	}
	if pkg.Type == "" {
		pkg.Type = "charm"
	}
	if pkg.Status == "" {
		pkg.Status = "registered"
	}
	if pkg.OwnerAccountID == "" {
		pkg.OwnerAccountID = owner.ID
	}
	if pkg.CreatedAt.IsZero() {
		pkg.CreatedAt = now
	}
	if pkg.UpdatedAt.IsZero() {
		pkg.UpdatedAt = now
	}
	err := repository.CreatePackage(context.Background(), pkg)
	require.NoError(t, err)
	return pkg
}

func TestRepositoryCanManagePackageViaGroupACL(t *testing.T) {
	repository := newRepositoryBehaviorTestRepository(t)
	ctx := context.Background()
	owner := ensureRepositoryBehaviorTestAccount(t, repository, "owner-1", "owner")
	editor := ensureRepositoryBehaviorTestAccount(t, repository, "editor-1", "editor")
	pkg := createRepositoryBehaviorTestPackage(t, repository, owner, core.Package{
		ID:      "pkg-manage",
		Name:    "manage-me",
		Private: true,
	})
	_, err := repository.db.ExecContext(ctx, `
		INSERT INTO account_groups (id, slug, display_name, created_at)
		VALUES (?, ?, ?, ?)
	`, "group-1", "editors", "Editors", time.Now().UTC())
	require.NoError(t, err)
	_, err = repository.db.ExecContext(ctx, `
		INSERT INTO account_group_members (group_id, account_id) VALUES (?, ?)
	`, "group-1", editor.ID)
	require.NoError(t, err)
	_, err = repository.db.ExecContext(ctx, `
		INSERT INTO package_acl (package_id, principal_type, principal_id, role)
		VALUES (?, 'group', ?, 'editor')
	`, pkg.ID, "group-1")
	require.NoError(t, err)

	canManage, err := repository.CanManagePackage(ctx, pkg.ID, editor.ID)
	require.NoError(t, err)
	assert.True(t, canManage)

}

func TestRepositoryCanViewPackageViaGroupACL(t *testing.T) {
	repository := newRepositoryBehaviorTestRepository(t)
	ctx := context.Background()
	owner := ensureRepositoryBehaviorTestAccount(t, repository, "owner-2", "owner2")
	viewer := ensureRepositoryBehaviorTestAccount(t, repository, "viewer-1", "viewer")
	pkg := createRepositoryBehaviorTestPackage(t, repository, owner, core.Package{
		ID:      "pkg-view",
		Name:    "view-me",
		Private: true,
	})
	_, err := repository.db.ExecContext(ctx, `
		INSERT INTO account_groups (id, slug, display_name, created_at)
		VALUES (?, ?, ?, ?)
	`, "group-2", "viewers", "Viewers", time.Now().UTC())
	require.NoError(t, err)
	_, err = repository.db.ExecContext(ctx, `
		INSERT INTO account_group_members (group_id, account_id) VALUES (?, ?)
	`, "group-2", viewer.ID)
	require.NoError(t, err)
	_, err = repository.db.ExecContext(ctx, `
		INSERT INTO package_acl (package_id, principal_type, principal_id, role)
		VALUES (?, 'group', ?, 'viewer')
	`, pkg.ID, "group-2")
	require.NoError(t, err)

	canView, err := repository.CanViewPackage(ctx, pkg.ID, viewer.ID)
	require.NoError(t, err)
	assert.True(t, canView)

	canManage, err := repository.CanManagePackage(ctx, pkg.ID, viewer.ID)
	require.NoError(t, err)
	assert.False(t, canManage)

}

func TestRepositoryResolveDefaultReleaseFallback(t *testing.T) {
	repository := newRepositoryBehaviorTestRepository(t)
	ctx := context.Background()
	owner := ensureRepositoryBehaviorTestAccount(t, repository, "owner-3", "owner3")
	pkg := createRepositoryBehaviorTestPackage(t, repository, owner, core.Package{
		ID:           "pkg-release",
		Name:         "release-me",
		DefaultTrack: stringPtr("2.0"),
	})
	older := time.Now().UTC().Add(-time.Hour)
	newer := time.Now().UTC()
	require.NoError(t, repository.ReplaceRelease(ctx, pkg.ID, core.Release{
		ID:       "rel-1",
		Channel:  "latest/edge",
		Revision: 1,
		When:     older,
	}))
	require.NoError(t, repository.ReplaceRelease(ctx, pkg.ID, core.Release{
		ID:       "rel-2",
		Channel:  "1.0/stable",
		Revision: 2,
		When:     newer,
	}))

	release, err := repository.ResolveDefaultRelease(ctx, pkg.ID)
	require.NoError(t, err)
	assert.Equal(t, "1.0/stable", release.Channel)
	assert.Equal(t, 2, release.Revision)

}

func TestRepositoryWithinTransactionRollsBackOnError(t *testing.T) {
	repository := newRepositoryBehaviorTestRepository(t)
	ctx := context.Background()
	owner := ensureRepositoryBehaviorTestAccount(t, repository, "owner-4", "owner4")
	err := repository.WithinTransaction(ctx, func(txRepo CompositeRepo) error {
		return txRepo.CreatePackage(ctx, core.Package{
			ID:             "pkg-tx",
			Name:           "tx-package",
			Type:           "charm",
			Status:         "registered",
			OwnerAccountID: owner.ID,
			CreatedAt:      time.Now().UTC(),
			UpdatedAt:      time.Now().UTC(),
		})
	})
	require.NoError(t, err)

	err = repository.WithinTransaction(ctx, func(txRepo CompositeRepo) error {
		if err := txRepo.CreatePackage(ctx, core.Package{
			ID:             "pkg-rollback",
			Name:           "rollback-package",
			Type:           "charm",
			Status:         "registered",
			OwnerAccountID: owner.ID,
			CreatedAt:      time.Now().UTC(),
			UpdatedAt:      time.Now().UTC(),
		}); err != nil {
			return err
		}
		return fmt.Errorf("force rollback")
	})
	require.EqualError(t, err, "force rollback")

	_, err = repository.GetPackageByName(ctx, "rollback-package")
	require.ErrorIs(t, err, ErrNotFound)

}

func TestRepositoryPushRevisionStyleTransactionRollsBackOnUpdatePackageFailure(t *testing.T) {

	repository := newRepositoryBehaviorTestRepository(t)
	ctx := context.Background()
	owner := ensureRepositoryBehaviorTestAccount(t, repository, "owner-push-revision", "owner-push-revision")
	now := time.Now().UTC()
	pkg := createRepositoryBehaviorTestPackage(t, repository, owner, core.Package{
		ID:   "pkg-push-revision-tx",
		Name: "push-revision-tx",
	})
	upload := core.Upload{
		ID:        "upload-push-revision-tx",
		Filename:  "push-revision-tx.charm",
		ObjectKey: "uploads/push-revision-tx.charm",
		Size:      123,
		SHA256:    "sha256",
		SHA384:    "sha384",
		Status:    "pending",
		Kind:      "revision",
		CreatedAt: now,
	}
	require.NoError(t, repository.CreateUpload(ctx, upload))
	pkg.Status = "published"
	pkg.UpdatedAt = now.Add(time.Minute)
	revisionNumber := 1

	err := repository.WithinTransaction(ctx, func(txRepo CompositeRepo) error {
		failingTx := repositoryBehaviorUpdatePackageFailingRepository{CompositeRepo: txRepo, err: assert.AnError}
		if err := failingTx.CreateRevision(ctx, core.Revision{
			ID:        "rev-push-revision-tx",
			PackageID: pkg.ID,
			Revision:  revisionNumber,
			Version:   "1",
			Status:    "approved",
			CreatedAt: now,
			CreatedBy: owner.ID,
			Size:      upload.Size,
			SHA256:    upload.SHA256,
			SHA384:    upload.SHA384,
			ObjectKey: upload.ObjectKey,
		}); err != nil {
			return err
		}
		if err := failingTx.ApproveUpload(ctx, upload.ID, &revisionNumber, nil); err != nil {
			return err
		}
		return failingTx.UpdatePackage(ctx, pkg)
	})
	require.ErrorIs(t, err, assert.AnError)

	_, err = repository.GetRevisionByNumber(ctx, pkg.ID, revisionNumber)
	require.ErrorIs(t, err, ErrNotFound)

	storedUpload, err := repository.GetUpload(ctx, upload.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", storedUpload.Status)
	assert.Nil(t, storedUpload.ApprovedAt)
	assert.Nil(t, storedUpload.Revision)
}

func TestRepositorySearchPackagesEscapesWildcards(t *testing.T) {
	repository := newRepositoryBehaviorTestRepository(t)
	ctx := context.Background()
	owner := ensureRepositoryBehaviorTestAccount(t, repository, "owner-search", "owner-search")
	createRepositoryBehaviorTestPackage(t, repository, owner, core.Package{
		ID:   "pkg-percent",
		Name: "literal%name",
	})
	createRepositoryBehaviorTestPackage(t, repository, owner, core.Package{
		ID:   "pkg-underscore",
		Name: "literal_name",
	})
	createRepositoryBehaviorTestPackage(t, repository, owner, core.Package{
		ID:   "pkg-plain",
		Name: "literalxname",
	})
	percentMatches, err := repository.SearchPackages(ctx, "%")
	require.NoError(t, err)
	require.Len(t, percentMatches, 1)
	assert.Equal(t, "literal%name", percentMatches[0].Name)

	underscoreMatches, err := repository.SearchPackages(ctx, "_")
	require.NoError(t, err)
	require.Len(t, underscoreMatches, 1)
	assert.Equal(t, "literal_name", underscoreMatches[0].Name)

}

func TestRepositoryCharmhubSyncRuleCRUD(t *testing.T) {
	repository := newRepositoryBehaviorTestRepository(t)
	ctx := context.Background()
	admin := ensureRepositoryBehaviorTestAccount(t, repository, "admin-sync", "admin-sync")
	now := time.Now().UTC()
	err := repository.CreateCharmhubSyncRule(ctx, core.CharmhubSyncRule{
		PackageName:        "demo",
		Track:              "latest",
		Bases:              []string{"ubuntu@24.04"},
		Architectures:      []string{"amd64", "arm64"},
		CreatedByAccountID: admin.ID,
		CreatedAt:          now,
		UpdatedAt:          now,
		LastSyncStatus:     "pending",
	})
	require.NoError(t, err)

	rules, err := repository.ListCharmhubSyncRules(ctx)
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, "demo", rules[0].PackageName)
	assert.Equal(t, "latest", rules[0].Track)
	assert.Equal(t, []string{"ubuntu@24.04"}, rules[0].Bases)
	assert.Equal(t, []string{"amd64", "arm64"}, rules[0].Architectures)

	startedAt := now.Add(time.Minute)
	finishedAt := now.Add(2 * time.Minute)
	lastError := "sync failed"
	rule := rules[0]
	rule.LastSyncStatus = "error"
	rule.LastSyncStartedAt = &startedAt
	rule.LastSyncFinishedAt = &finishedAt
	rule.LastSyncError = &lastError
	rule.UpdatedAt = now.Add(3 * time.Minute)
	require.NoError(t, repository.UpdateCharmhubSyncRule(ctx, rule))

	rules, err = repository.ListCharmhubSyncRulesByPackageName(ctx, "demo")
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, "error", rules[0].LastSyncStatus)
	require.NotNil(t, rules[0].LastSyncError)
	assert.Equal(t, "sync failed", *rules[0].LastSyncError)

	require.NoError(t, repository.DeleteCharmhubSyncRule(ctx, "demo", "latest"))
	rules, err = repository.ListCharmhubSyncRulesByPackageName(ctx, "demo")
	require.NoError(t, err)
	assert.Empty(t, rules)

}

func TestRepositoryReleaseVariantsByBase(t *testing.T) {
	repository := newRepositoryBehaviorTestRepository(t)
	ctx := context.Background()
	owner := ensureRepositoryBehaviorTestAccount(t, repository, "owner-release-variant", "owner-release-variant")
	pkg := createRepositoryBehaviorTestPackage(t, repository, owner, core.Package{
		ID:   "pkg-release-variant",
		Name: "release-variant",
	})
	require.NoError(t, repository.ReplaceRelease(ctx, pkg.ID, core.Release{
		ID:       "rel-amd64",
		Channel:  "latest/stable",
		Revision: 1,
		Base:     &core.Base{Name: "ubuntu", Channel: "24.04", Architecture: "amd64"},
		When:     time.Now().UTC(),
	}))
	require.NoError(t, repository.ReplaceRelease(ctx, pkg.ID, core.Release{
		ID:       "rel-arm64",
		Channel:  "latest/stable",
		Revision: 2,
		Base:     &core.Base{Name: "ubuntu", Channel: "24.04", Architecture: "arm64"},
		When:     time.Now().UTC().Add(time.Minute),
	}))
	releases, err := repository.ListReleases(ctx, pkg.ID)
	require.NoError(t, err)
	require.Len(t, releases, 2)

	amd64Release, err := repository.ResolveReleaseForBase(
		ctx,
		pkg.ID,
		"latest/stable",
		core.Base{Name: "ubuntu", Channel: "24.04", Architecture: "amd64"},
	)
	require.NoError(t, err)
	assert.Equal(t, 1, amd64Release.Revision)

	latestRelease, err := repository.ResolveRelease(ctx, pkg.ID, "latest/stable")
	require.NoError(t, err)
	assert.Equal(t, 2, latestRelease.Revision)

	require.NoError(t, repository.DeleteReleaseForBase(
		ctx,
		pkg.ID,
		"latest/stable",
		&core.Base{Name: "ubuntu", Channel: "24.04", Architecture: "amd64"},
	))
	releases, err = repository.ListReleases(ctx, pkg.ID)
	require.NoError(t, err)
	require.Len(t, releases, 1)
	assert.Equal(t, 2, releases[0].Revision)

}

func TestRepositoryDeletePrimitivesForSyncCleanup(t *testing.T) {
	repository := newRepositoryBehaviorTestRepository(t)
	ctx := context.Background()
	owner := ensureRepositoryBehaviorTestAccount(t, repository, "owner-sync-delete", "owner-sync-delete")
	pkg := createRepositoryBehaviorTestPackage(t, repository, owner, core.Package{
		ID:   "pkg-sync-delete",
		Name: "sync-delete",
	})
	_, err := repository.CreateTracks(ctx, pkg.ID, []core.Track{{
		Name:      "latest",
		CreatedAt: time.Now().UTC(),
	}})
	require.NoError(t, err)

	require.NoError(t, repository.CreateRevision(ctx, core.Revision{
		ID:        "rev-1",
		PackageID: pkg.ID,
		Revision:  1,
		Version:   "1",
		Status:    "approved",
		CreatedAt: time.Now().UTC(),
		CreatedBy: owner.ID,
		ObjectKey: "charms/pkg-sync-delete/1.charm",
	}))

	resourceDef, err := repository.UpsertResourceDefinition(ctx, core.ResourceDefinition{
		ID:          "res-def-1",
		PackageID:   pkg.ID,
		Name:        "app-image",
		Type:        "oci-image",
		Description: "image",
		CreatedAt:   time.Now().UTC(),
	})
	require.NoError(t, err)

	require.NoError(t, repository.CreateResourceRevision(ctx, core.ResourceRevision{
		ID:              "res-rev-1",
		ResourceID:      resourceDef.ID,
		Name:            "app-image",
		Type:            "oci-image",
		Revision:        1,
		CreatedAt:       time.Now().UTC(),
		OCIImageDigest:  "sha256:deadbeef",
		PackageRevision: intPtr(1),
	}))

	require.NoError(t, repository.ReplaceRelease(ctx, pkg.ID, core.Release{
		ID:       "rel-1",
		Channel:  "latest/stable",
		Revision: 1,
		Resources: []core.ReleaseResourceRef{{
			Name:     "app-image",
			Revision: intPtr(1),
		}},
		When: time.Now().UTC(),
	}))
	require.NoError(t, repository.DeleteRelease(ctx, pkg.ID, "latest/stable"))
	_, err = repository.ResolveRelease(ctx, pkg.ID, "latest/stable")
	require.ErrorIs(t, err, ErrNotFound)

	require.NoError(t, repository.DeleteTrack(ctx, pkg.ID, "latest"))
	tracks, err := repository.ListTracks(ctx, pkg.ID)
	require.NoError(t, err)
	assert.Empty(t, tracks)

	require.NoError(t, repository.DeleteResourceRevision(ctx, resourceDef.ID, 1))
	_, err = repository.GetResourceRevision(ctx, resourceDef.ID, 1)
	require.ErrorIs(t, err, ErrNotFound)

	require.NoError(t, repository.DeleteResourceDefinition(ctx, resourceDef.ID))
	_, err = repository.GetResourceDefinition(ctx, pkg.ID, "app-image")
	require.ErrorIs(t, err, ErrNotFound)

	require.NoError(t, repository.DeleteRevision(ctx, pkg.ID, 1))
	_, err = repository.GetRevisionByNumber(ctx, pkg.ID, 1)
	require.ErrorIs(t, err, ErrNotFound)

}

func stringPtr(value string) *string {
	return &value
}

type repositoryBehaviorUpdatePackageFailingRepository struct {
	CompositeRepo
	err error
}

func (r repositoryBehaviorUpdatePackageFailingRepository) UpdatePackage(_ context.Context, _ core.Package) error {
	return r.err
}

func intPtr(value int) *int {
	return &value
}
