package repo

import (
	"context"
	"embed"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Postgres is a PostgreSQL-backed [Backend].
type Postgres struct {
	pool *pgxpool.Pool
	db   postgresDB
}

type postgresDB interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// NewPostgres opens a PostgreSQL-backed [Backend].
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
// Uses a schema_migrations table for version tracking and an advisory lock
// (pg_advisory_lock) to prevent concurrent migration runs from racing.
//
// The following errors may be returned:
// - Errors from reading embedded migrations.
// - Errors from executing migration statements.
func (p *Postgres) Migrate(ctx context.Context) error {
	// Ensure the schema_migrations tracking table exists.
	if _, err := p.pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version TEXT PRIMARY KEY,
    applied_at TIMESTAMP NOT NULL
)`); err != nil {
		return fmt.Errorf("cannot create schema_migrations table: %w", err)
	}

	// Acquire an advisory lock to serialize concurrent migration attempts.
	const lockClass, lockObjID = 1, 0
	var locked bool
	if err := p.pool.QueryRow(ctx,
		"SELECT pg_try_advisory_lock($1, $2)", lockClass, lockObjID).Scan(&locked); err != nil {
		return fmt.Errorf("cannot acquire migration advisory lock: %w", err)
	}
	if !locked {
		return fmt.Errorf("migration advisory lock is held by another process — skipping")
	}
	defer p.pool.QueryRow(ctx, "SELECT pg_advisory_unlock($1, $2)", lockClass, lockObjID).Scan(nil)

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		// Skip already-applied migrations.
		var applied string
		err := p.pool.QueryRow(ctx,
			"SELECT version FROM schema_migrations WHERE version = $1", entry.Name()).Scan(&applied)
		if err == nil {
			continue
		}
		if err != pgx.ErrNoRows {
			return fmt.Errorf("cannot check migration status for %s: %w", entry.Name(), err)
		}

		payload, err := migrationsFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return err
		}
		// Apply each migration inside a transaction and record it.
		if err := p.WithinTransaction(ctx, func(repository CompositeRepo) error {
			pgRepo := repository.(*Postgres)
			if _, err := pgRepo.db.Exec(ctx, string(payload)); err != nil {
				return fmt.Errorf("cannot apply migration %s: %w", entry.Name(), err)
			}
			_, err := pgRepo.db.Exec(ctx,
				"INSERT INTO schema_migrations (version, applied_at) VALUES ($1, $2)",
				entry.Name(), time.Now().UTC())
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

// Ping is part of the [HealthRepo] interface.
func (p *Postgres) Ping(ctx context.Context) error {
	return p.pool.Ping(ctx)
}

// Close releases the PostgreSQL connection pool.
func (p *Postgres) Close() error {
	p.pool.Close()
	return nil
}

// WithinTransaction is part of the [Transactor] interface.
func (p *Postgres) WithinTransaction(ctx context.Context, fn func(CompositeRepo) error) error {
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
