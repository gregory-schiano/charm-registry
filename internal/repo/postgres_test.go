package repo

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/gschiano/charm-registry/internal/core"
)

var (
	postgresTestContainer *tcpostgres.PostgresContainer
	postgresTestDSN       string
	postgresTestErr       error
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	postgresTestContainer, postgresTestErr = tcpostgres.Run(
		ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("registry"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if postgresTestErr == nil {
		postgresTestDSN, postgresTestErr = postgresTestContainer.ConnectionString(ctx, "sslmode=disable")
	}

	code := m.Run()

	if postgresTestContainer != nil {
		if err := postgresTestContainer.Terminate(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "terminate postgres test container: %v\n", err)
		}
	}
	os.Exit(code)
}

func newPostgresIntegrationRepository(t *testing.T) *Postgres {
	t.Helper()
	if postgresTestErr != nil {
		t.Skipf("postgres integration environment unavailable: %v", postgresTestErr)
	}

	ctx := context.Background()
	repository, err := NewPostgres(ctx, postgresTestDSN)
	require.NoError(t, err)
	t.Cleanup(repository.pool.Close)

	_, err = repository.pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`)
	require.NoError(t, err)
	require.NoError(t, repository.Migrate(ctx))

	return repository
}

func ensureTestAccount(t *testing.T, repository *Postgres, id, username string) core.Account {
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

func createTestPackage(t *testing.T, repository *Postgres, owner core.Account, pkg core.Package) core.Package {
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

func TestPostgresCanManagePackageViaGroupACL(t *testing.T) {
	repository := newPostgresIntegrationRepository(t)
	ctx := context.Background()

	owner := ensureTestAccount(t, repository, "owner-1", "owner")
	editor := ensureTestAccount(t, repository, "editor-1", "editor")
	pkg := createTestPackage(t, repository, owner, core.Package{
		ID:      "pkg-manage",
		Name:    "manage-me",
		Private: true,
	})

	_, err := repository.pool.Exec(ctx, `
		INSERT INTO account_groups (id, slug, display_name, created_at)
		VALUES ($1, $2, $3, $4)
	`, "group-1", "editors", "Editors", time.Now().UTC())
	require.NoError(t, err)
	_, err = repository.pool.Exec(ctx, `
		INSERT INTO account_group_members (group_id, account_id) VALUES ($1, $2)
	`, "group-1", editor.ID)
	require.NoError(t, err)
	_, err = repository.pool.Exec(ctx, `
		INSERT INTO package_acl (package_id, principal_type, principal_id, role)
		VALUES ($1, 'group', $2, 'editor')
	`, pkg.ID, "group-1")
	require.NoError(t, err)

	canManage, err := repository.CanManagePackage(ctx, pkg.ID, editor.ID)
	require.NoError(t, err)
	assert.True(t, canManage)
}

func TestPostgresCanViewPackageViaGroupACL(t *testing.T) {
	repository := newPostgresIntegrationRepository(t)
	ctx := context.Background()

	owner := ensureTestAccount(t, repository, "owner-2", "owner2")
	viewer := ensureTestAccount(t, repository, "viewer-1", "viewer")
	pkg := createTestPackage(t, repository, owner, core.Package{
		ID:      "pkg-view",
		Name:    "view-me",
		Private: true,
	})

	_, err := repository.pool.Exec(ctx, `
		INSERT INTO account_groups (id, slug, display_name, created_at)
		VALUES ($1, $2, $3, $4)
	`, "group-2", "viewers", "Viewers", time.Now().UTC())
	require.NoError(t, err)
	_, err = repository.pool.Exec(ctx, `
		INSERT INTO account_group_members (group_id, account_id) VALUES ($1, $2)
	`, "group-2", viewer.ID)
	require.NoError(t, err)
	_, err = repository.pool.Exec(ctx, `
		INSERT INTO package_acl (package_id, principal_type, principal_id, role)
		VALUES ($1, 'group', $2, 'viewer')
	`, pkg.ID, "group-2")
	require.NoError(t, err)

	canView, err := repository.CanViewPackage(ctx, pkg.ID, viewer.ID)
	require.NoError(t, err)
	assert.True(t, canView)

	canManage, err := repository.CanManagePackage(ctx, pkg.ID, viewer.ID)
	require.NoError(t, err)
	assert.False(t, canManage)
}

func TestPostgresResolveDefaultReleaseFallback(t *testing.T) {
	repository := newPostgresIntegrationRepository(t)
	ctx := context.Background()

	owner := ensureTestAccount(t, repository, "owner-3", "owner3")
	pkg := createTestPackage(t, repository, owner, core.Package{
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

func TestPostgresWithinTransactionRollsBackOnError(t *testing.T) {
	repository := newPostgresIntegrationRepository(t)
	ctx := context.Background()

	owner := ensureTestAccount(t, repository, "owner-4", "owner4")
	err := repository.WithinTransaction(ctx, func(txRepo Repository) error {
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

	err = repository.WithinTransaction(ctx, func(txRepo Repository) error {
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

func TestPostgresSearchPackagesEscapesWildcards(t *testing.T) {
	repository := newPostgresIntegrationRepository(t)
	ctx := context.Background()

	owner := ensureTestAccount(t, repository, "owner-search", "owner-search")
	createTestPackage(t, repository, owner, core.Package{
		ID:   "pkg-percent",
		Name: "literal%name",
	})
	createTestPackage(t, repository, owner, core.Package{
		ID:   "pkg-underscore",
		Name: "literal_name",
	})
	createTestPackage(t, repository, owner, core.Package{
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

func stringPtr(value string) *string {
	return &value
}
