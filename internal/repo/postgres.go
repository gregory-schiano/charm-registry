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
// Acquires a single dedicated connection from the pool and uses it for the
// entire lock → migrate → unlock sequence. This avoids the previous bug
// where pool.QueryRow could execute the lock and unlock on different
// physical connections, silently orphaning the advisory lock.
//
// Uses pg_advisory_lock (blocking) so concurrent startup units wait for
// each other instead of immediately failing with "lock is held by another
// process — skipping".
//
// The following errors may be returned:
// - Errors from acquiring a connection from the pool.
// - Errors from reading embedded migrations.
// - Errors from executing migration statements.
func (p *Postgres) Migrate(ctx context.Context) error {
	// Acquire a single dedicated connection for the entire migration sequence.
	// All lock, query, and transaction operations below use this connection
	// to guarantee the advisory lock stays on the same session.
	conn, err := p.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("cannot acquire connection for migration: %w", err)
	}
	defer conn.Release()

	// Ensure the schema_migrations tracking table exists.
	if _, err := conn.Exec(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version TEXT PRIMARY KEY,
    applied_at TIMESTAMP NOT NULL
)`); err != nil {
		return fmt.Errorf("cannot create schema_migrations table: %w", err)
	}

	// Acquire a blocking advisory lock to serialize concurrent migration
	// attempts. Unlike pg_try_advisory_lock, this waits instead of returning
	// false, which avoids crash loops when multiple units start simultaneously.
	const lockClass, lockObjID = 1, 0
	if _, err := conn.Exec(ctx,
		"SELECT pg_advisory_lock($1, $2)", lockClass, lockObjID); err != nil {
		return fmt.Errorf("cannot acquire migration advisory lock: %w", err)
	}
	// Best-effort unlock before the deferred conn.Release(). If the unlock
	// fails the lock is still released when the connection is returned to
	// the pool and eventually closed.
	defer func() {
		_, _ = conn.Exec(ctx, "SELECT pg_advisory_unlock($1, $2)", lockClass, lockObjID)
	}()

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		// Skip already-applied migrations.
		var applied string
		err := conn.QueryRow(ctx,
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
		// Apply each migration inside a transaction on the same dedicated
		// connection that holds the advisory lock.
		tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return fmt.Errorf("cannot begin transaction for migration %s: %w", entry.Name(), err)
		}
		txRepo := &Postgres{pool: p.pool, db: tx}
		if _, err := txRepo.db.Exec(ctx, string(payload)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("cannot apply migration %s: %w", entry.Name(), err)
		}
		if _, err := txRepo.db.Exec(ctx,
			"INSERT INTO schema_migrations (version, applied_at) VALUES ($1, $2)",
			entry.Name(), time.Now().UTC()); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("cannot record migration %s: %w", entry.Name(), err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("cannot commit migration %s: %w", entry.Name(), err)
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
