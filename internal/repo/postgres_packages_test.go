package repo

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gschiano/charm-registry/internal/core"
)

type mockPostgresDB struct {
	execErr error
}

func (m mockPostgresDB) Exec(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, m.execErr
}

func (m mockPostgresDB) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	return nil, errors.New("unexpected query")
}

func (m mockPostgresDB) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	return nil
}

func TestPostgresCreatePackageMapsUniqueViolationToConflict(t *testing.T) {
	t.Parallel()

	// Arrange
	repository := &Postgres{db: mockPostgresDB{
		execErr: &pgconn.PgError{Code: "23505"},
	}}

	// Act
	err := repository.CreatePackage(context.Background(), core.Package{
		ID:             "pkg-1",
		Name:           "demo",
		Type:           "charm",
		Status:         "registered",
		OwnerAccountID: "acc-1",
	})

	// Assert
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrConflict)
	assert.Contains(t, err.Error(), "cannot create package")

}

func TestPostgresCreatePackageReturnsOriginalErrorWhenNotUniqueViolation(t *testing.T) {
	t.Parallel()

	// Arrange
	execErr := errors.New("boom")
	repository := &Postgres{db: mockPostgresDB{execErr: execErr}}

	// Act
	err := repository.CreatePackage(context.Background(), core.Package{
		ID:             "pkg-1",
		Name:           "demo",
		Type:           "charm",
		Status:         "registered",
		OwnerAccountID: "acc-1",
	})

	// Assert
	require.Error(t, err)
	assert.ErrorIs(t, err, execErr)

}
