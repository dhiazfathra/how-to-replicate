// Package db provides pgx/v5 pool construction, a health check, and a
// transaction helper shared by every Go service.
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

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
