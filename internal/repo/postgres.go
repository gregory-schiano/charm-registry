package repo

import (
	"context"
	"embed"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Postgres is a PostgreSQL-backed [Repository].
type Postgres struct {
	pool *pgxpool.Pool
	db   postgresDB
}

type postgresDB interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
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
	return &Postgres{pool: pool, db: pool}, nil
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

// Ping is part of the [Repository] interface.
func (p *Postgres) Ping(ctx context.Context) error {
	return p.pool.Ping(ctx)
}

// Close releases the PostgreSQL connection pool.
func (p *Postgres) Close() error {
	p.pool.Close()
	return nil
}

// WithinTransaction is part of the [Repository] interface.
func (p *Postgres) WithinTransaction(ctx context.Context, fn func(Repository) error) error {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	txRepo := &Postgres{pool: p.pool, db: tx}
	if err := fn(txRepo); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}
