package repo

import (
	"context"
	"embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Postgres is a PostgreSQL-backed [Repository].
type Postgres struct {
	pool *pgxpool.Pool
}

// NewPostgres opens a PostgreSQL-backed [Repository].
//
// The following errors may be returned:
// - Errors from creating the PostgreSQL connection pool.
func NewPostgres(ctx context.Context, databaseURL string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	return &Postgres{pool: pool}, nil
}

// Migrate applies the embedded repository schema migrations.
//
// The following errors may be returned:
// - Errors from reading embedded migrations.
// - Errors from executing migration statements.
func (p *Postgres) Migrate(ctx context.Context) error {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		payload, err := migrationsFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return err
		}
		if _, err := p.pool.Exec(ctx, string(payload)); err != nil {
			return fmt.Errorf("cannot apply migration %s: %w", entry.Name(), err)
		}
	}
	return nil
}
