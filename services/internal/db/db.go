// Package db provides pgx/v5 pool construction, a health check, and a
// transaction helper shared by every Go service.
package db

import (
	"context"
	"fmt"
	"net/url"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RuntimeRole is the Postgres role every service connects as. It holds
// SELECT/INSERT on every table and UPDATE/DELETE only on tables that are
// genuinely mutated in place — never on the append-only tables. The
// migration role (whatever superuser/owner ran goose) is never used for
// service traffic; see services/internal/migrate/migrations/00002_schema.sql.
const RuntimeRole = "htr_runtime"

// WithRuntimeRole rewrites dsn's credentials to connect as RuntimeRole,
// so every service talks to Postgres as the least-privileged role instead
// of the schema owner. password is the runtime role's password.
func WithRuntimeRole(dsn, password string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("db: parse dsn: %w", err)
	}
	u.User = url.UserPassword(RuntimeRole, password)
	return u.String(), nil
}

// Pool is the subset of *pgxpool.Pool used by services. Defined as an
// interface so callers can fake it in tests without a real database.
type Pool interface {
	Ping(ctx context.Context) error
	Begin(ctx context.Context) (pgx.Tx, error)
	Close()
}

// newWithConfig is pgxpool.NewWithConfig by default; tests override it to
// exercise NewPool's error path, since a config produced by ParseConfig
// essentially never fails construction in practice.
var newWithConfig = pgxpool.NewWithConfig

// NewPool parses dsn and constructs a connection pool. It does not ping the
// database; call HealthCheck for that.
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("db: parse dsn: %w", err)
	}

	pool, err := newWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: new pool: %w", err)
	}

	return pool, nil
}

// HealthCheck pings the pool, returning a wrapped error on failure.
func HealthCheck(ctx context.Context, pool Pool) error {
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("db: health check: %w", err)
	}
	return nil
}

// WithTx runs fn inside a transaction, committing on success and rolling
// back on error or panic.
func WithTx(ctx context.Context, pool Pool, fn func(ctx context.Context, tx pgx.Tx) error) (err error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db: begin tx: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return
		}
		err = tx.Commit(ctx)
	}()

	err = fn(ctx, tx)
	return err
}
