package repo

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/gschiano/charm-registry/internal/core"
)

func newSQLiteTestRepository(t *testing.T) *SQLite {
	t.Helper()

	repository, err := NewSQLite(context.Background(), t.TempDir()+"/registry.sqlite")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, repository.Close())
	})
	require.NoError(t, repository.Migrate(context.Background()))
	return repository
}

func ensureSQLiteAccount(t *testing.T, repository *SQLite, id, username string) core.Account {
	t.Helper()

	account, err := repository.EnsureAccount(context.Background(), core.Account{
		ID:          id,
		Subject:     username,
		Username:    username,
		DisplayName: username,
		Email:       username + "@example.com",
		Validation:  "verified",
		CreatedAt:   time.Now().UTC(),
	})
	require.NoError(t, err)
	return account
}

func TestSQLitePersistsCoreRepositoryData(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repository := newSQLiteTestRepository(t)
	owner := ensureSQLiteAccount(t, repository, "acc-1", "owner")
	now := time.Now().UTC()

	pkg := core.Package{
		ID:             "pkg-1",
		Name:           "demo",
		Type:           "charm",
		Private:        true,
		Status:         "registered",
		OwnerAccountID: owner.ID,
		Links:          map[string][]string{"docs": {"https://example.com/docs"}},
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	require.NoError(t, repository.CreatePackage(ctx, pkg))

	stored, err := repository.GetPackageByName(ctx, "demo")
	require.NoError(t, err)
	assert.Equal(t, pkg.ID, stored.ID)
	assert.Equal(t, owner.Username, stored.Publisher.Username)
	assert.Equal(t, pkg.Links, stored.Links)

	require.NoError(t, repository.CreateRevision(ctx, core.Revision{
		ID:        "rev-1",
		PackageID: pkg.ID,
		Revision:  1,
		Version:   "1",
		Status:    "approved",
		CreatedAt: now,
		CreatedBy: owner.ID,
		Size:      12,
		SHA256:    "sha256",
		SHA384:    "sha384",
		ObjectKey: "uploads/rev-1",
		Bases:     []core.Base{{Name: "ubuntu", Channel: "24.04", Architecture: "amd64"}},
		Attributes: map[string]string{
			"framework": "operator",
		},
	}))

	latest, err := repository.GetLatestRevision(ctx, pkg.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, latest.Revision)
	assert.Equal(t, []core.Base{{Name: "ubuntu", Channel: "24.04", Architecture: "amd64"}}, latest.Bases)
}

func TestSQLiteCreatePackageMapsUniqueViolationToConflict(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repository := newSQLiteTestRepository(t)
	owner := ensureSQLiteAccount(t, repository, "acc-1", "owner")
	now := time.Now().UTC()
	pkg := core.Package{
		ID:             "pkg-1",
		Name:           "demo",
		Type:           "charm",
		Status:         "registered",
		OwnerAccountID: owner.ID,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	require.NoError(t, repository.CreatePackage(ctx, pkg))

	duplicate := pkg
	duplicate.ID = "pkg-2"
	err := repository.CreatePackage(ctx, duplicate)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrConflict)
	assert.Contains(t, err.Error(), "cannot create package")
}

func TestSQLiteCreatePackageReturnsOriginalErrorForForeignKeyViolation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repository := newSQLiteTestRepository(t)
	now := time.Now().UTC()
	err := repository.CreatePackage(ctx, core.Package{
		ID:             "pkg-1",
		Name:           "demo",
		Type:           "charm",
		Status:         "registered",
		OwnerAccountID: "missing-account",
		CreatedAt:      now,
		UpdatedAt:      now,
	})

	require.Error(t, err)
	var sqliteErr *sqlite.Error
	require.ErrorAs(t, err, &sqliteErr)
	assert.Equal(t, sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY, sqliteErr.Code())
	assert.NotErrorIs(t, err, ErrConflict)
}

func TestSQLiteReleasesUseBaseScopedUniqueness(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repository := newSQLiteTestRepository(t)
	owner := ensureSQLiteAccount(t, repository, "acc-1", "owner")
	pkg := core.Package{
		ID:             "pkg-1",
		Name:           "demo",
		Type:           "charm",
		Status:         "registered",
		OwnerAccountID: owner.ID,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	require.NoError(t, repository.CreatePackage(ctx, pkg))

	amd64 := &core.Base{Name: "ubuntu", Channel: "24.04", Architecture: "amd64"}
	arm64 := &core.Base{Name: "ubuntu", Channel: "24.04", Architecture: "arm64"}
	require.NoError(t, repository.ReplaceRelease(ctx, pkg.ID, core.Release{ID: "rel-1", Channel: "latest/stable", Revision: 1, Base: amd64, When: time.Now().UTC()}))
	require.NoError(t, repository.ReplaceRelease(ctx, pkg.ID, core.Release{ID: "rel-2", Channel: "latest/stable", Revision: 2, Base: arm64, When: time.Now().UTC()}))
	require.NoError(t, repository.ReplaceRelease(ctx, pkg.ID, core.Release{ID: "rel-3", Channel: "latest/stable", Revision: 3, Base: amd64, When: time.Now().UTC()}))

	releases, err := repository.ListReleases(ctx, pkg.ID)
	require.NoError(t, err)
	require.Len(t, releases, 2)

	resolved, err := repository.ResolveReleaseForBase(ctx, pkg.ID, "latest/stable", *amd64)
	require.NoError(t, err)
	assert.Equal(t, 3, resolved.Revision)
}

func TestSQLiteTransactionRollsBack(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repository := newSQLiteTestRepository(t)
	owner := ensureSQLiteAccount(t, repository, "acc-1", "owner")

	err := repository.WithinTransaction(ctx, func(tx CompositeRepo) error {
		return tx.CreatePackage(ctx, core.Package{
			ID:             "pkg-rollback",
			Name:           "rollback",
			Type:           "charm",
			Status:         "registered",
			OwnerAccountID: owner.ID,
			CreatedAt:      time.Now().UTC(),
			UpdatedAt:      time.Now().UTC(),
		})
	})
	require.NoError(t, err)

	err = repository.WithinTransaction(ctx, func(tx CompositeRepo) error {
		if err := tx.CreatePackage(ctx, core.Package{
			ID:             "pkg-rollback-2",
			Name:           "rollback-2",
			Type:           "charm",
			Status:         "registered",
			OwnerAccountID: owner.ID,
			CreatedAt:      time.Now().UTC(),
			UpdatedAt:      time.Now().UTC(),
		}); err != nil {
			return err
		}
		return assert.AnError
	})
	require.Error(t, err)

	_, err = repository.GetPackageByName(ctx, "rollback-2")
	assert.ErrorIs(t, err, ErrNotFound)
}
