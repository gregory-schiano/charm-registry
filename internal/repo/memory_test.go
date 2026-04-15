package repo

import (
	"context"
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

	// Arrange
	ctx := context.Background()
	m := NewMemory()

	// Act
	acc := core.Account{
		ID: "acc-1", Subject: "sub-1", Username: "user1",
		DisplayName: "User One", Email: "u@e.com", Validation: "verified",
	}
	got, err := m.EnsureAccount(ctx, acc)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, "acc-1", got.ID)
	assert.Equal(t, "user1", got.Username)

}

func TestMemoryEnsureAccountUpdatesExistingFields(t *testing.T) {
	t.Parallel()

	// Arrange
	ctx := context.Background()
	m := NewMemory()

	initial := core.Account{
		ID: "acc-1", Subject: "sub-1", Username: "user1",
		DisplayName: "Old Name", Email: "old@e.com",
	}
	_, _ = m.EnsureAccount(ctx, initial)

	// Act
	updated := core.Account{
		ID: "acc-1", Subject: "sub-1", Username: "newuser",
		DisplayName: "New Name", Email: "new@e.com",
	}
	got, err := m.EnsureAccount(ctx, updated)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, "acc-1", got.ID, "ID must not change on upsert")
	assert.Equal(t, "New Name", got.DisplayName)
	assert.Equal(t, "new@e.com", got.Email)
	assert.Equal(t, "newuser", got.Username)

}

func TestMemoryMethodsReturnNotFoundForMissingKeys(t *testing.T) {
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
		{"DeletePackage", func() error { return m.DeletePackage(ctx, "x") }},
		{"GetPackageByName", func() error { _, err := m.GetPackageByName(ctx, "x"); return err }},
		{"GetPackageByID", func() error { _, err := m.GetPackageByID(ctx, "x"); return err }},
		{"CanViewPackage", func() error { _, err := m.CanViewPackage(ctx, "x", "acc"); return err }},
		{"CanManagePackage", func() error { _, err := m.CanManagePackage(ctx, "x", "acc"); return err }},
		{"CreateTracks", func() error { _, err := m.CreateTracks(ctx, "x", []core.Track{{Name: "latest"}}); return err }},
		{"ListTracks", func() error { _, err := m.ListTracks(ctx, "x"); return err }},
		{"ApproveUpload", func() error { return m.ApproveUpload(ctx, "x", nil, nil) }},
		{"GetUpload", func() error { _, err := m.GetUpload(ctx, "x"); return err }},
		{"GetRevisionByNumber", func() error { _, err := m.GetRevisionByNumber(ctx, "x", 1); return err }},
		{"GetLatestRevision", func() error { _, err := m.GetLatestRevision(ctx, "x"); return err }},
		{"GetResourceDefinition", func() error { _, err := m.GetResourceDefinition(ctx, "x", "r"); return err }},
		{"UpdateResourceRevision", func() error {
			return m.UpdateResourceRevision(ctx, core.ResourceRevision{ResourceID: "x", Revision: 1})
		}},
		{"GetResourceRevision", func() error { _, err := m.GetResourceRevision(ctx, "x", 1); return err }},
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

	// Arrange
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

	// Act
	gotToken, gotAcc, err := m.FindStoreTokenByHash(ctx, "hash-abc")

	// Assert
	require.NoError(t, err)
	assert.Equal(t, "sess-1", gotToken.SessionID)
	assert.Equal(t, acc.ID, gotAcc.ID)

}

func TestMemoryFindStoreTokenMissingAccount(t *testing.T) {
	t.Parallel()

	// Arrange
	ctx := context.Background()
	m := NewMemory()

	// Token references an account that doesn't exist.
	_ = m.CreateStoreToken(ctx, core.StoreToken{
		SessionID: "sess-1", TokenHash: "hash-1", AccountID: "ghost",
		ValidSince: time.Now().UTC(), ValidUntil: time.Now().UTC().Add(time.Hour),
	})

	// Act
	_, _, err := m.FindStoreTokenByHash(ctx, "hash-1")

	// Assert
	assert.ErrorIs(t, err, ErrNotFound)

}

func TestMemoryListStoreTokensFiltersExpired(t *testing.T) {
	t.Parallel()

	// Arrange
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

	// Act
	tokens, err := m.ListStoreTokens(ctx, acc.ID, false)

	// Assert
	require.NoError(t, err)
	require.Len(t, tokens, 1)
	assert.Equal(t, "active", tokens[0].SessionID)

}

func TestMemoryListStoreTokensIncludesInactiveWhenFlagSet(t *testing.T) {
	t.Parallel()

	// Arrange
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

	// Act
	tokens, err := m.ListStoreTokens(ctx, acc.ID, true)

	// Assert
	require.NoError(t, err)
	assert.Len(t, tokens, 2)

}

func TestMemoryListStoreTokensFiltersRevoked(t *testing.T) {
	t.Parallel()

	// Arrange
	ctx := context.Background()
	m, acc := memWithAccount(t)

	_ = m.CreateStoreToken(ctx, core.StoreToken{
		SessionID: "sess-1", TokenHash: "h1", AccountID: acc.ID,
		ValidSince: time.Now().UTC(), ValidUntil: time.Now().UTC().Add(time.Hour),
	})
	require.NoError(t, m.RevokeStoreToken(ctx, acc.ID, "sess-1", acc.ID))

	// Act
	tokens, err := m.ListStoreTokens(ctx, acc.ID, false)

	// Assert
	require.NoError(t, err)
	assert.Empty(t, tokens)

}

func TestMemoryRevokeStoreTokenNotFound(t *testing.T) {
	t.Parallel()

	// Arrange
	m, acc := memWithAccount(t)

	// Act
	err := m.RevokeStoreToken(context.Background(), acc.ID, "nonexistent", acc.ID)

	// Assert
	assert.ErrorIs(t, err, ErrNotFound)

}

// ---- Packages --------------------------------------------------------------

func TestMemoryCreatePackageAndGetByNameAndID(t *testing.T) {
	t.Parallel()

	// Arrange
	ctx := context.Background()
	m, acc := memWithAccount(t)
	pkg := core.Package{ID: "p1", Name: "mycharm", OwnerAccountID: acc.ID}
	require.NoError(t, m.CreatePackage(ctx, pkg))

	// Act + Assert
	byName, err := m.GetPackageByName(ctx, "mycharm")
	require.NoError(t, err)
	assert.Equal(t, "p1", byName.ID)

	byID, err := m.GetPackageByID(ctx, "p1")
	require.NoError(t, err)
	assert.Equal(t, "mycharm", byID.Name)

}

func TestMemoryCreatePackageConflict(t *testing.T) {
	t.Parallel()

	// Arrange
	ctx := context.Background()
	m, acc := memWithAccount(t)
	require.NoError(t, m.CreatePackage(ctx, core.Package{ID: "p1", Name: "mycharm", OwnerAccountID: acc.ID}))

	// Act
	err := m.CreatePackage(ctx, core.Package{ID: "p2", Name: "mycharm", OwnerAccountID: acc.ID})

	// Assert
	assert.ErrorIs(t, err, ErrConflict)

}

func TestMemoryUpdatePackage(t *testing.T) {
	t.Parallel()

	// Arrange
	ctx := context.Background()
	m, acc := memWithAccount(t)
	require.NoError(t, m.CreatePackage(ctx, core.Package{ID: "p1", Name: "mycharm", OwnerAccountID: acc.ID}))

	// Act
	desc := "updated description"
	err := m.UpdatePackage(ctx, core.Package{ID: "p1", Name: "mycharm", OwnerAccountID: acc.ID, Description: &desc})
	require.NoError(t, err)

	// Assert
	got, err := m.GetPackageByName(ctx, "mycharm")
	require.NoError(t, err)
	require.NotNil(t, got.Description)
	assert.Equal(t, "updated description", *got.Description)

}

func TestMemoryDeletePackage(t *testing.T) {
	t.Parallel()

	// Arrange
	ctx := context.Background()
	m, acc := memWithAccount(t)
	require.NoError(t, m.CreatePackage(ctx, core.Package{ID: "p1", Name: "mycharm", OwnerAccountID: acc.ID}))

	// Act
	require.NoError(t, m.DeletePackage(ctx, "p1"))

	// Assert
	_, err := m.GetPackageByName(ctx, "mycharm")
	assert.ErrorIs(t, err, ErrNotFound)

}

func TestMemoryListPackagesForAccount(t *testing.T) {
	t.Parallel()

	// Arrange
	ctx := context.Background()
	m, acc := memWithAccount(t)

	_ = m.CreatePackage(ctx, core.Package{ID: "p1", Name: "charm-a", OwnerAccountID: acc.ID})
	_ = m.CreatePackage(ctx, core.Package{ID: "p2", Name: "charm-b", OwnerAccountID: "other"})

	// Act
	pkgs, err := m.ListPackagesForAccount(ctx, acc.ID, false)

	// Assert
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Equal(t, "charm-a", pkgs[0].Name)

}

func TestMemorySearchPackagesEmptyQueryReturnsAll(t *testing.T) {
	t.Parallel()

	// Arrange
	ctx := context.Background()
	m, acc := memWithAccount(t)

	_ = m.CreatePackage(ctx, core.Package{ID: "p1", Name: "alpha", OwnerAccountID: acc.ID})
	_ = m.CreatePackage(ctx, core.Package{ID: "p2", Name: "beta", OwnerAccountID: acc.ID})

	// Act
	pkgs, err := m.SearchPackages(ctx, "")

	// Assert
	require.NoError(t, err)
	assert.Len(t, pkgs, 2)

}

func TestMemorySearchPackagesCaseInsensitive(t *testing.T) {
	t.Parallel()

	// Arrange
	ctx := context.Background()
	m, acc := memWithAccount(t)

	_ = m.CreatePackage(ctx, core.Package{ID: "p1", Name: "MyCharm", OwnerAccountID: acc.ID})
	_ = m.CreatePackage(ctx, core.Package{ID: "p2", Name: "other", OwnerAccountID: acc.ID})

	// Act
	pkgs, err := m.SearchPackages(ctx, "mycharm")

	// Assert
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Equal(t, "MyCharm", pkgs[0].Name)

}

func TestMemoryCanViewPackagePublic(t *testing.T) {
	t.Parallel()

	// Arrange
	ctx := context.Background()
	m, acc := memWithAccount(t)

	_ = m.CreatePackage(ctx, core.Package{ID: "p1", Name: "pub", OwnerAccountID: acc.ID, Private: false})

	// Act
	can, err := m.CanViewPackage(ctx, "p1", "anyone")

	// Assert
	require.NoError(t, err)
	assert.True(t, can)

}

func TestMemoryCanViewPackagePrivateOwner(t *testing.T) {
	t.Parallel()

	// Arrange
	ctx := context.Background()
	m, acc := memWithAccount(t)

	_ = m.CreatePackage(ctx, core.Package{ID: "p1", Name: "priv", OwnerAccountID: acc.ID, Private: true})

	// Act
	can, err := m.CanViewPackage(ctx, "p1", acc.ID)

	// Assert
	require.NoError(t, err)
	assert.True(t, can)

}

func TestMemoryCanViewPackagePrivateNonOwner(t *testing.T) {
	t.Parallel()

	// Arrange
	ctx := context.Background()
	m, acc := memWithAccount(t)

	_ = m.CreatePackage(ctx, core.Package{ID: "p1", Name: "priv", OwnerAccountID: acc.ID, Private: true})

	// Act
	can, err := m.CanViewPackage(ctx, "p1", "stranger")

	// Assert
	require.NoError(t, err)
	assert.False(t, can)

}

func TestMemoryCanManagePackageOwnerVsStranger(t *testing.T) {
	t.Parallel()

	// Arrange
	ctx := context.Background()
	m, acc := memWithAccount(t)

	// Act
	_ = m.CreatePackage(ctx, core.Package{ID: "p1", Name: "charm", OwnerAccountID: acc.ID})

	// Assert
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

	// Arrange
	ctx := context.Background()
	m, acc := memWithAccount(t)
	_ = m.CreatePackage(ctx, core.Package{ID: "p1", Name: "charm", OwnerAccountID: acc.ID})

	// Act + Assert
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

	// Arrange
	ctx := context.Background()
	m := NewMemory()
	require.NoError(t, m.CreateUpload(ctx, core.Upload{ID: "up-1", Status: "pending"}))

	// Act
	rev := 5
	require.NoError(t, m.ApproveUpload(ctx, "up-1", &rev, nil))

	// Assert
	got, err := m.GetUpload(ctx, "up-1")
	require.NoError(t, err)
	assert.Equal(t, "approved", got.Status)
	assert.Equal(t, &rev, got.Revision)
	assert.NotNil(t, got.ApprovedAt)

}

func TestMemoryUploadRejected(t *testing.T) {
	t.Parallel()

	// Arrange
	ctx := context.Background()
	m := NewMemory()
	require.NoError(t, m.CreateUpload(ctx, core.Upload{ID: "up-1"}))

	// Act
	errs := []core.APIError{{Code: "bad-file", Message: "corrupt archive"}}
	require.NoError(t, m.ApproveUpload(ctx, "up-1", nil, errs))

	// Assert
	got, err := m.GetUpload(ctx, "up-1")
	require.NoError(t, err)
	assert.Equal(t, "rejected", got.Status)
	assert.Equal(t, errs, got.Errors)

}

// ---- Revisions -------------------------------------------------------------

func TestMemoryRevisionRoundtrip(t *testing.T) {
	t.Parallel()

	// Arrange
	ctx := context.Background()
	m := NewMemory()

	// Act
	_ = m.CreateRevision(ctx, core.Revision{PackageID: "p1", Revision: 1})
	_ = m.CreateRevision(ctx, core.Revision{PackageID: "p1", Revision: 2})

	// Assert
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

	// Arrange
	ctx := context.Background()
	m := NewMemory()

	_ = m.CreateRevision(ctx, core.Revision{PackageID: "p1", Revision: 1})

	// Act
	n := 99
	_, err := m.ListRevisions(ctx, "p1", &n)

	// Assert
	assert.ErrorIs(t, err, ErrNotFound)

}

// ---- Resource definitions --------------------------------------------------

func TestMemoryResourceDefinitionUpsert(t *testing.T) {
	t.Parallel()

	// Arrange
	ctx := context.Background()
	m := NewMemory()
	res := core.ResourceDefinition{PackageID: "p1", Name: "config", Type: "file"}

	// Act
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

	// Arrange
	ctx := context.Background()
	m := NewMemory()
	rev := core.ResourceRevision{ResourceID: "res-1", Revision: 1, CreatedAt: time.Now().UTC()}
	require.NoError(t, m.CreateResourceRevision(ctx, rev))

	// Act + Assert
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

	// Arrange
	ctx := context.Background()
	m := NewMemory()

	// Act
	_ = m.ReplaceRelease(ctx, "p1", core.Release{Channel: "latest/stable", Revision: 5, When: time.Now().UTC()})
	_ = m.ReplaceRelease(ctx, "p1", core.Release{Channel: "latest/edge", Revision: 7, When: time.Now().UTC()})

	// Assert
	all, err := m.ListReleases(ctx, "p1")
	require.NoError(t, err)
	assert.Len(t, all, 2)

	stable, err := m.ResolveRelease(ctx, "p1", "latest/stable")
	require.NoError(t, err)
	assert.Equal(t, 5, stable.Revision)

}

func TestMemoryReleaseReplaceUpdatesChannel(t *testing.T) {
	t.Parallel()

	// Arrange
	ctx := context.Background()
	m := NewMemory()

	// Act
	_ = m.ReplaceRelease(ctx, "p1", core.Release{Channel: "latest/stable", Revision: 1})
	_ = m.ReplaceRelease(ctx, "p1", core.Release{Channel: "latest/stable", Revision: 2})

	// Assert
	got, err := m.ResolveRelease(ctx, "p1", "latest/stable")
	require.NoError(t, err)
	assert.Equal(t, 2, got.Revision, "ReplaceRelease must overwrite existing entry")

}

func TestMemoryResolveDefaultReleasePreferStable(t *testing.T) {
	t.Parallel()

	// Arrange
	ctx := context.Background()
	m := NewMemory()

	_ = m.ReplaceRelease(ctx, "p1", core.Release{Channel: "latest/edge", Revision: 1})
	_ = m.ReplaceRelease(ctx, "p1", core.Release{Channel: "latest/stable", Revision: 5})

	// Act
	release, err := m.ResolveDefaultRelease(ctx, "p1")

	// Assert
	require.NoError(t, err)
	assert.Equal(t, 5, release.Revision, "should prefer latest/stable")

}

func TestMemoryResolveDefaultReleaseFallback(t *testing.T) {
	t.Parallel()

	// Arrange
	ctx := context.Background()
	m := NewMemory()

	_ = m.ReplaceRelease(ctx, "p1", core.Release{Channel: "latest/edge", Revision: 2})

	// Act
	release, err := m.ResolveDefaultRelease(ctx, "p1")

	// Assert
	require.NoError(t, err)
	assert.Equal(t, 2, release.Revision, "should fall back to any release when no latest/stable")

}

