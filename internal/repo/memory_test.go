package repo

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gschiano/charm-registry/internal/core"
)

// memWithAccount returns a fresh Memory repo pre-seeded with one account.
func memWithAccount(t *testing.T) (*Memory, core.Account) {
	t.Helper()
	ctx := context.Background()
	m := NewMemory()
	acc := core.Account{
		ID:          "acc-1",
		Subject:     "sub-1",
		Username:    "user1",
		DisplayName: "User One",
		Email:       "user1@example.com",
		Validation:  "verified",
		CreatedAt:   time.Now().UTC(),
	}
	got, err := m.EnsureAccount(ctx, acc)
	require.NoError(t, err)
	return m, got
}

// ---- Account ---------------------------------------------------------------

func TestMemoryEnsureAccountCreate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMemory()
	acc := core.Account{
		ID: "acc-1", Subject: "sub-1", Username: "user1",
		DisplayName: "User One", Email: "u@e.com", Validation: "verified",
	}
	got, err := m.EnsureAccount(ctx, acc)
	require.NoError(t, err)
	assert.Equal(t, "acc-1", got.ID)
	assert.Equal(t, "user1", got.Username)

}

func TestMemoryConcurrentPackageAccess(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	m, owner := memWithAccount(t)
	var wg sync.WaitGroup
	errs := make(chan error, 40)

	for i := range 20 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- m.CreatePackage(ctx, core.Package{
				ID:             fmt.Sprintf("pkg-%d", i),
				Name:           fmt.Sprintf("package-%d", i),
				Type:           "charm",
				Status:         "registered",
				OwnerAccountID: owner.ID,
				CreatedAt:      time.Now().UTC(),
				UpdatedAt:      time.Now().UTC(),
			})
		}(i)
	}
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := m.ListPackagesForAccount(ctx, owner.ID, false)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
}

func TestMemoryConcurrentCreateRevisionSamePackage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	m, owner := memWithAccount(t)
	require.NoError(t, m.CreatePackage(ctx, core.Package{
		ID:             "pkg-1",
		Name:           "package-1",
		Type:           "charm",
		Status:         "registered",
		OwnerAccountID: owner.ID,
		CreatedAt:      time.Unix(0, 0).UTC(),
		UpdatedAt:      time.Unix(0, 0).UTC(),
	}))

	const revisions = 20
	var wg sync.WaitGroup
	errs := make(chan error, revisions)
	for revision := 1; revision <= revisions; revision++ {
		revision := revision
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- m.CreateRevision(ctx, core.Revision{
				ID:        fmt.Sprintf("rev-%d", revision),
				PackageID: "pkg-1",
				Revision:  revision,
				Version:   fmt.Sprintf("%d", revision),
				Status:    "approved",
				CreatedAt: time.Unix(int64(revision), 0).UTC(),
				CreatedBy: owner.ID,
			})
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}

	stored, err := m.ListRevisions(ctx, "pkg-1", nil)
	require.NoError(t, err)
	assert.Len(t, stored, revisions)
	seen := make(map[int]struct{}, len(stored))
	for _, revision := range stored {
		seen[revision.Revision] = struct{}{}
	}
	for revision := 1; revision <= revisions; revision++ {
		_, ok := seen[revision]
		assert.True(t, ok)
	}

	require.NoError(t, m.CreateRevision(ctx, core.Revision{
		ID:        "rev-21",
		PackageID: "pkg-1",
		Revision:  revisions + 1,
		Version:   fmt.Sprintf("%d", revisions+1),
		Status:    "approved",
		CreatedAt: time.Unix(revisions+1, 0).UTC(),
		CreatedBy: owner.ID,
	}))

	latest, err := m.GetLatestRevision(ctx, "pkg-1")
	require.NoError(t, err)
	assert.Equal(t, revisions+1, latest.Revision)
}

func TestMemoryMaintenanceAndDeleteOperations(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	m, owner := memWithAccount(t)
	require.NoError(t, m.Ping(ctx))
	require.NoError(t, m.Migrate(ctx))

	now := time.Unix(0, 0).UTC()
	pkg := core.Package{
		ID:             "pkg-delete",
		Name:           "delete-me",
		Type:           "charm",
		Status:         "registered",
		OwnerAccountID: owner.ID,
		CreatedAt:      now,
		UpdatedAt:      now,
		Tracks:         []core.Track{{Name: "latest", CreatedAt: now}, {Name: "2.0", CreatedAt: now}},
	}
	require.NoError(t, m.CreatePackage(ctx, pkg))
	require.NoError(t, m.DeleteTrack(ctx, pkg.ID, "2.0"))
	tracks, err := m.ListTracks(ctx, pkg.ID)
	require.NoError(t, err)
	assert.Len(t, tracks, 1)

	require.NoError(t, m.CreateRevision(ctx, core.Revision{PackageID: pkg.ID, Revision: 1}))
	require.NoError(t, m.CreateRevision(ctx, core.Revision{PackageID: pkg.ID, Revision: 2}))
	require.NoError(t, m.DeleteRevision(ctx, pkg.ID, 1))
	_, err = m.GetRevisionByNumber(ctx, pkg.ID, 1)
	assert.ErrorIs(t, err, ErrNotFound)

	require.NoError(t, m.ReplaceRelease(ctx, pkg.ID, core.Release{ID: "rel-1", Channel: "latest/stable", Revision: 2, When: now}))
	require.NoError(t, m.DeleteRelease(ctx, pkg.ID, "latest/stable"))
	_, err = m.ResolveRelease(ctx, pkg.ID, "latest/stable")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestMemoryEnsureAccountUpdatesExistingFields(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMemory()

	initial := core.Account{
		ID: "acc-1", Subject: "sub-1", Username: "user1",
		DisplayName: "Old Name", Email: "old@e.com",
	}
	_, _ = m.EnsureAccount(ctx, initial)
	updated := core.Account{
		ID: "acc-1", Subject: "sub-1", Username: "newuser",
		DisplayName: "New Name", Email: "new@e.com",
	}
	got, err := m.EnsureAccount(ctx, updated)
	require.NoError(t, err)
	assert.Equal(t, "acc-1", got.ID, "ID must not change on upsert")
	assert.Equal(t, "New Name", got.DisplayName)
	assert.Equal(t, "new@e.com", got.Email)
	assert.Equal(t, "newuser", got.Username)

}

func TestMemoryRepresentativeMethodsReturnNotFoundForMissingKeys(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	m := NewMemory()

	tests := []struct {
		name string
		fn   func() error
	}{
		{"GetAccountByID", func() error { _, err := m.GetAccountByID(ctx, "x"); return err }},
		{"FindStoreTokenByHash", func() error { _, _, err := m.FindStoreTokenByHash(ctx, "x"); return err }},
		{"UpdatePackage", func() error { return m.UpdatePackage(ctx, core.Package{Name: "x"}) }},
		{"CreateTracks", func() error { _, err := m.CreateTracks(ctx, "x", []core.Track{{Name: "latest"}}); return err }},
		{"ApproveUpload", func() error { return m.ApproveUpload(ctx, "x", nil, nil) }},
		{"GetRevisionByNumber", func() error { _, err := m.GetRevisionByNumber(ctx, "x", 1); return err }},
		{"GetResourceDefinition", func() error { _, err := m.GetResourceDefinition(ctx, "x", "r"); return err }},
		{"UpdateResourceRevision", func() error {
			return m.UpdateResourceRevision(ctx, core.ResourceRevision{ResourceID: "x", Revision: 1})
		}},
		{"ResolveRelease", func() error { _, err := m.ResolveRelease(ctx, "x", "latest/stable"); return err }},
		{"ResolveDefaultRelease", func() error { _, err := m.ResolveDefaultRelease(ctx, "x"); return err }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.ErrorIs(t, tt.fn(), ErrNotFound)
		})
	}
}

// ---- Store tokens ----------------------------------------------------------

func TestMemoryStoreTokenRoundtrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, acc := memWithAccount(t)

	token := core.StoreToken{
		SessionID:  "sess-1",
		TokenHash:  "hash-abc",
		AccountID:  acc.ID,
		ValidSince: time.Now().UTC(),
		ValidUntil: time.Now().UTC().Add(time.Hour),
	}
	require.NoError(t, m.CreateStoreToken(ctx, token))
	gotToken, gotAcc, err := m.FindStoreTokenByHash(ctx, "hash-abc")
	require.NoError(t, err)
	assert.Equal(t, "sess-1", gotToken.SessionID)
	assert.Equal(t, acc.ID, gotAcc.ID)

}

func TestMemoryFindStoreTokenMissingAccount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMemory()

	// Token references an account that doesn't exist.
	_ = m.CreateStoreToken(ctx, core.StoreToken{
		SessionID: "sess-1", TokenHash: "hash-1", AccountID: "ghost",
		ValidSince: time.Now().UTC(), ValidUntil: time.Now().UTC().Add(time.Hour),
	})
	_, _, err := m.FindStoreTokenByHash(ctx, "hash-1")
	assert.ErrorIs(t, err, ErrNotFound)

}

func TestMemoryListStoreTokensFiltersExpired(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, acc := memWithAccount(t)

	past := time.Now().UTC().Add(-2 * time.Hour)
	future := time.Now().UTC().Add(time.Hour)

	_ = m.CreateStoreToken(ctx, core.StoreToken{
		SessionID: "active", TokenHash: "h1", AccountID: acc.ID,
		ValidSince: past, ValidUntil: future,
	})
	_ = m.CreateStoreToken(ctx, core.StoreToken{
		SessionID: "expired", TokenHash: "h2", AccountID: acc.ID,
		ValidSince: past.Add(-time.Hour), ValidUntil: past,
	})
	tokens, err := m.ListStoreTokens(ctx, acc.ID, false)
	require.NoError(t, err)
	require.Len(t, tokens, 1)
	assert.Equal(t, "active", tokens[0].SessionID)

}

func TestMemoryListStoreTokensIncludesInactiveWhenFlagSet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, acc := memWithAccount(t)

	past := time.Now().UTC().Add(-2 * time.Hour)
	future := time.Now().UTC().Add(time.Hour)

	_ = m.CreateStoreToken(ctx, core.StoreToken{
		SessionID: "active", TokenHash: "h1", AccountID: acc.ID,
		ValidSince: past, ValidUntil: future,
	})
	_ = m.CreateStoreToken(ctx, core.StoreToken{
		SessionID: "expired", TokenHash: "h2", AccountID: acc.ID,
		ValidSince: past.Add(-time.Hour), ValidUntil: past,
	})
	tokens, err := m.ListStoreTokens(ctx, acc.ID, true)
	require.NoError(t, err)
	assert.Len(t, tokens, 2)

}

func TestMemoryListStoreTokensFiltersRevoked(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, acc := memWithAccount(t)

	_ = m.CreateStoreToken(ctx, core.StoreToken{
		SessionID: "sess-1", TokenHash: "h1", AccountID: acc.ID,
		ValidSince: time.Now().UTC(), ValidUntil: time.Now().UTC().Add(time.Hour),
	})
	require.NoError(t, m.RevokeStoreToken(ctx, acc.ID, "sess-1", acc.ID))
	tokens, err := m.ListStoreTokens(ctx, acc.ID, false)
	require.NoError(t, err)
	assert.Empty(t, tokens)

}

func TestMemoryRevokeStoreTokenNotFound(t *testing.T) {
	t.Parallel()
	m, acc := memWithAccount(t)
	err := m.RevokeStoreToken(context.Background(), acc.ID, "nonexistent", acc.ID)
	assert.ErrorIs(t, err, ErrNotFound)

}

// ---- Packages --------------------------------------------------------------

func TestMemoryCreatePackageAndGetByNameAndID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, acc := memWithAccount(t)
	pkg := core.Package{ID: "p1", Name: "mycharm", OwnerAccountID: acc.ID}
	require.NoError(t, m.CreatePackage(ctx, pkg))
	byName, err := m.GetPackageByName(ctx, "mycharm")
	require.NoError(t, err)
	assert.Equal(t, "p1", byName.ID)

	byID, err := m.GetPackageByID(ctx, "p1")
	require.NoError(t, err)
	assert.Equal(t, "mycharm", byID.Name)

}

func TestMemoryCreatePackageConflict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, acc := memWithAccount(t)
	require.NoError(t, m.CreatePackage(ctx, core.Package{ID: "p1", Name: "mycharm", OwnerAccountID: acc.ID}))
	err := m.CreatePackage(ctx, core.Package{ID: "p2", Name: "mycharm", OwnerAccountID: acc.ID})
	assert.ErrorIs(t, err, ErrConflict)

}

func TestMemoryUpdatePackage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, acc := memWithAccount(t)
	require.NoError(t, m.CreatePackage(ctx, core.Package{ID: "p1", Name: "mycharm", OwnerAccountID: acc.ID}))
	desc := "updated description"
	err := m.UpdatePackage(ctx, core.Package{ID: "p1", Name: "mycharm", OwnerAccountID: acc.ID, Description: &desc})
	require.NoError(t, err)
	got, err := m.GetPackageByName(ctx, "mycharm")
	require.NoError(t, err)
	require.NotNil(t, got.Description)
	assert.Equal(t, "updated description", *got.Description)

}

func TestMemoryDeletePackage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, acc := memWithAccount(t)
	require.NoError(t, m.CreatePackage(ctx, core.Package{ID: "p1", Name: "mycharm", OwnerAccountID: acc.ID}))
	require.NoError(t, m.DeletePackage(ctx, "p1"))
	_, err := m.GetPackageByName(ctx, "mycharm")
	assert.ErrorIs(t, err, ErrNotFound)

}

func TestMemoryListPackagesForAccount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, acc := memWithAccount(t)

	_ = m.CreatePackage(ctx, core.Package{ID: "p1", Name: "charm-a", OwnerAccountID: acc.ID})
	_ = m.CreatePackage(ctx, core.Package{ID: "p2", Name: "charm-b", OwnerAccountID: "other"})
	pkgs, err := m.ListPackagesForAccount(ctx, acc.ID, false)
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Equal(t, "charm-a", pkgs[0].Name)

}

func TestMemorySearchPackagesEmptyQueryReturnsAll(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, acc := memWithAccount(t)

	_ = m.CreatePackage(ctx, core.Package{ID: "p1", Name: "alpha", OwnerAccountID: acc.ID})
	_ = m.CreatePackage(ctx, core.Package{ID: "p2", Name: "beta", OwnerAccountID: acc.ID})
	pkgs, err := m.SearchPackages(ctx, "")
	require.NoError(t, err)
	assert.Len(t, pkgs, 2)

}

func TestMemorySearchPackagesCaseInsensitive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, acc := memWithAccount(t)

	_ = m.CreatePackage(ctx, core.Package{ID: "p1", Name: "MyCharm", OwnerAccountID: acc.ID})
	_ = m.CreatePackage(ctx, core.Package{ID: "p2", Name: "other", OwnerAccountID: acc.ID})
	pkgs, err := m.SearchPackages(ctx, "mycharm")
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Equal(t, "MyCharm", pkgs[0].Name)

}

func TestMemoryCanViewPackagePublic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, acc := memWithAccount(t)

	_ = m.CreatePackage(ctx, core.Package{ID: "p1", Name: "pub", OwnerAccountID: acc.ID, Private: false})
	can, err := m.CanViewPackage(ctx, "p1", "anyone")
	require.NoError(t, err)
	assert.True(t, can)

}

func TestMemoryCanViewPackagePrivateOwner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, acc := memWithAccount(t)

	_ = m.CreatePackage(ctx, core.Package{ID: "p1", Name: "priv", OwnerAccountID: acc.ID, Private: true})
	can, err := m.CanViewPackage(ctx, "p1", acc.ID)
	require.NoError(t, err)
	assert.True(t, can)

}

func TestMemoryCanViewPackagePrivateNonOwner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, acc := memWithAccount(t)

	_ = m.CreatePackage(ctx, core.Package{ID: "p1", Name: "priv", OwnerAccountID: acc.ID, Private: true})
	can, err := m.CanViewPackage(ctx, "p1", "stranger")
	require.NoError(t, err)
	assert.False(t, can)

}

func TestMemoryCanManagePackageOwnerVsStranger(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, acc := memWithAccount(t)
	_ = m.CreatePackage(ctx, core.Package{ID: "p1", Name: "charm", OwnerAccountID: acc.ID})
	canOwner, err := m.CanManagePackage(ctx, "p1", acc.ID)
	require.NoError(t, err)
	assert.True(t, canOwner)

	canOther, err := m.CanManagePackage(ctx, "p1", "stranger")
	require.NoError(t, err)
	assert.False(t, canOther)

}

// ---- Tracks ----------------------------------------------------------------

func TestMemoryCreateTracksDeduplicates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, acc := memWithAccount(t)
	_ = m.CreatePackage(ctx, core.Package{ID: "p1", Name: "charm", OwnerAccountID: acc.ID})
	n, err := m.CreateTracks(ctx, "p1", []core.Track{{Name: "latest"}, {Name: "1.0"}})
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	// Re-inserting existing + one new — only the new one should be counted.
	n2, err := m.CreateTracks(ctx, "p1", []core.Track{{Name: "latest"}, {Name: "2.0"}})
	require.NoError(t, err)
	assert.Equal(t, 1, n2)

	tracks, err := m.ListTracks(ctx, "p1")
	require.NoError(t, err)
	assert.Len(t, tracks, 3)

}

// ---- Uploads ---------------------------------------------------------------

func TestMemoryUploadApproved(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMemory()
	require.NoError(t, m.CreateUpload(ctx, core.Upload{ID: "up-1", Status: "pending"}))
	rev := 5
	require.NoError(t, m.ApproveUpload(ctx, "up-1", &rev, nil))
	got, err := m.GetUpload(ctx, "up-1")
	require.NoError(t, err)
	assert.Equal(t, "approved", got.Status)
	assert.Equal(t, &rev, got.Revision)
	assert.NotNil(t, got.ApprovedAt)

}

func TestMemoryUploadRejected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMemory()
	require.NoError(t, m.CreateUpload(ctx, core.Upload{ID: "up-1"}))
	errs := []core.APIError{{Code: "bad-file", Message: "corrupt archive"}}
	require.NoError(t, m.ApproveUpload(ctx, "up-1", nil, errs))
	got, err := m.GetUpload(ctx, "up-1")
	require.NoError(t, err)
	assert.Equal(t, "rejected", got.Status)
	assert.Equal(t, errs, got.Errors)

}

// ---- Revisions -------------------------------------------------------------

func TestMemoryRevisionRoundtrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMemory()
	_ = m.CreateRevision(ctx, core.Revision{PackageID: "p1", Revision: 1})
	_ = m.CreateRevision(ctx, core.Revision{PackageID: "p1", Revision: 2})
	// All revisions.
	all, err := m.ListRevisions(ctx, "p1", nil)
	require.NoError(t, err)
	assert.Len(t, all, 2)

	// Specific revision.
	n := 1
	one, err := m.ListRevisions(ctx, "p1", &n)
	require.NoError(t, err)
	require.Len(t, one, 1)
	assert.Equal(t, 1, one[0].Revision)

	// Get by number.
	got, err := m.GetRevisionByNumber(ctx, "p1", 2)
	require.NoError(t, err)
	assert.Equal(t, 2, got.Revision)

	// Latest.
	latest, err := m.GetLatestRevision(ctx, "p1")
	require.NoError(t, err)
	assert.Equal(t, 2, latest.Revision)

}

func TestMemoryListRevisionsByNumberNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMemory()

	_ = m.CreateRevision(ctx, core.Revision{PackageID: "p1", Revision: 1})
	n := 99
	_, err := m.ListRevisions(ctx, "p1", &n)
	assert.ErrorIs(t, err, ErrNotFound)

}

// ---- Resource definitions --------------------------------------------------

func TestMemoryResourceDefinitionUpsert(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMemory()
	res := core.ResourceDefinition{PackageID: "p1", Name: "config", Type: "file"}
	got, err := m.UpsertResourceDefinition(ctx, res)
	require.NoError(t, err)
	assert.Equal(t, "config", got.Name)

	// Upsert again with updated type.
	res.Type = "oci-image"
	got2, err := m.UpsertResourceDefinition(ctx, res)
	require.NoError(t, err)
	assert.Equal(t, "oci-image", got2.Type)

	listed, err := m.ListResourceDefinitions(ctx, "p1")
	require.NoError(t, err)
	assert.Len(t, listed, 1, "upsert must not create duplicates")

}

// ---- Resource revisions ----------------------------------------------------

func TestMemoryResourceRevisionRoundtrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMemory()
	rev := core.ResourceRevision{ResourceID: "res-1", Revision: 1, CreatedAt: time.Now().UTC()}
	require.NoError(t, m.CreateResourceRevision(ctx, rev))
	listed, err := m.ListResourceRevisions(ctx, "res-1")
	require.NoError(t, err)
	require.Len(t, listed, 1)

	got, err := m.GetResourceRevision(ctx, "res-1", 1)
	require.NoError(t, err)
	assert.Equal(t, 1, got.Revision)

	rev.Name = "updated-name"
	require.NoError(t, m.UpdateResourceRevision(ctx, rev))

	updated, err := m.GetResourceRevision(ctx, "res-1", 1)
	require.NoError(t, err)
	assert.Equal(t, "updated-name", updated.Name)

}

// ---- Releases --------------------------------------------------------------

func TestMemoryReleaseRoundtrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMemory()
	_ = m.ReplaceRelease(ctx, "p1", core.Release{Channel: "latest/stable", Revision: 5, When: time.Now().UTC()})
	_ = m.ReplaceRelease(ctx, "p1", core.Release{Channel: "latest/edge", Revision: 7, When: time.Now().UTC()})
	all, err := m.ListReleases(ctx, "p1")
	require.NoError(t, err)
	assert.Len(t, all, 2)

	stable, err := m.ResolveRelease(ctx, "p1", "latest/stable")
	require.NoError(t, err)
	assert.Equal(t, 5, stable.Revision)

}

func TestMemoryReleaseReplaceUpdatesChannel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMemory()
	_ = m.ReplaceRelease(ctx, "p1", core.Release{Channel: "latest/stable", Revision: 1})
	_ = m.ReplaceRelease(ctx, "p1", core.Release{Channel: "latest/stable", Revision: 2})
	got, err := m.ResolveRelease(ctx, "p1", "latest/stable")
	require.NoError(t, err)
	assert.Equal(t, 2, got.Revision, "ReplaceRelease must overwrite existing entry")

}

func TestMemoryResolveDefaultReleasePreferStable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMemory()

	_ = m.ReplaceRelease(ctx, "p1", core.Release{Channel: "latest/edge", Revision: 1})
	_ = m.ReplaceRelease(ctx, "p1", core.Release{Channel: "latest/stable", Revision: 5})
	release, err := m.ResolveDefaultRelease(ctx, "p1")
	require.NoError(t, err)
	assert.Equal(t, 5, release.Revision, "should prefer latest/stable")

}

func TestMemoryResolveDefaultReleaseFallback(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMemory()

	_ = m.ReplaceRelease(ctx, "p1", core.Release{Channel: "latest/edge", Revision: 2})
	release, err := m.ResolveDefaultRelease(ctx, "p1")
	require.NoError(t, err)
	assert.Equal(t, 2, release.Revision, "should fall back to any release when no latest/stable")

}

func TestMemoryCanViewPackageViaACL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, owner := memWithAccount(t)

	_ = m.CreatePackage(ctx, core.Package{ID: "p1", Name: "priv", OwnerAccountID: owner.ID, Private: true})

	// Viewer ACL entry grants view access.
	viewer := core.Account{ID: "viewer1", Subject: "viewer1"}
	_, _ = m.EnsureAccount(ctx, viewer)
	m.AddACLEntry("p1", "account", "viewer1", "viewer")

	canView, err := m.CanViewPackage(ctx, "p1", "viewer1")
	require.NoError(t, err)
	assert.True(t, canView, "viewer ACL should grant view access")

	canManage, err := m.CanManagePackage(ctx, "p1", "viewer1")
	require.NoError(t, err)
	assert.False(t, canManage, "viewer ACL should not grant manage access")
}

func TestMemoryCanManagePackageViaACL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, owner := memWithAccount(t)

	_ = m.CreatePackage(ctx, core.Package{ID: "p1", Name: "priv", OwnerAccountID: owner.ID, Private: true})

	editor := core.Account{ID: "editor1", Subject: "editor1"}
	_, _ = m.EnsureAccount(ctx, editor)
	m.AddACLEntry("p1", "account", "editor1", "editor")

	canView, err := m.CanViewPackage(ctx, "p1", "editor1")
	require.NoError(t, err)
	assert.True(t, canView, "editor ACL should grant view access")

	canManage, err := m.CanManagePackage(ctx, "p1", "editor1")
	require.NoError(t, err)
	assert.True(t, canManage, "editor ACL should grant manage access")
}

func TestMemoryCanViewPackageAnonymousPrivate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, acc := memWithAccount(t)

	_ = m.CreatePackage(ctx, core.Package{ID: "p1", Name: "priv", OwnerAccountID: acc.ID, Private: true})

	canView, err := m.CanViewPackage(ctx, "p1", "")
	require.NoError(t, err)
	assert.False(t, canView, "anonymous should not view private package")
}

func TestMemoryEnsureAccountRespectsCreatedAt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMemory()

	ts := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	acc := core.Account{ID: "a1", Subject: "sub1", CreatedAt: ts}

	got, err := m.EnsureAccount(ctx, acc)
	require.NoError(t, err)
	assert.Equal(t, ts, got.CreatedAt, "should preserve caller-provided CreatedAt")
}

func TestMemoryEnsureAccountDefaultsCreatedAt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMemory()

	acc := core.Account{ID: "a2", Subject: "sub2"}
	got, err := m.EnsureAccount(ctx, acc)
	require.NoError(t, err)
	assert.False(t, got.CreatedAt.IsZero(), "should default CreatedAt to now when zero")
}

func TestMemoryResolveDefaultReleasePicksHighestRevision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m, acc := memWithAccount(t)

	_ = m.CreatePackage(ctx, core.Package{ID: "p1", Name: "charm", OwnerAccountID: acc.ID})
	_ = m.ReplaceRelease(ctx, "p1", core.Release{Channel: "latest/edge", Revision: 5})
	_ = m.ReplaceRelease(ctx, "p1", core.Release{Channel: "latest/candidate", Revision: 3})

	release, err := m.ResolveDefaultRelease(ctx, "p1")
	require.NoError(t, err)
	assert.Equal(t, 5, release.Revision, "fallback should pick highest revision")
}
