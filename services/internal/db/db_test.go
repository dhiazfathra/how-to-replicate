package db

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type fakeTx struct {
	pgx.Tx
	committed  bool
	rolledBack bool
	commitErr  error
}

func (f *fakeTx) Commit(context.Context) error {
	f.committed = true
	return f.commitErr
}

func (f *fakeTx) Rollback(context.Context) error {
	f.rolledBack = true
	return nil
}

type fakePool struct {
	pingErr  error
	beginErr error
	tx       *fakeTx
	closed   bool
}

func (f *fakePool) Ping(context.Context) error { return f.pingErr }

func (f *fakePool) Begin(context.Context) (pgx.Tx, error) {
	if f.beginErr != nil {
		return nil, f.beginErr
	}
	return f.tx, nil
}

func (f *fakePool) Close() { f.closed = true }

func TestNewPool_InvalidDSN(t *testing.T) {
	if _, err := NewPool(context.Background(), "://not-a-dsn"); err == nil {
		t.Fatal("expected error for invalid dsn")
	}
}

func TestNewPool_Valid(t *testing.T) {
	pool, err := NewPool(context.Background(), "postgres://user:pass@localhost:5432/db")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pool.Close()
}

func TestNewPool_NewWithConfigError(t *testing.T) {
	original := newWithConfig
	t.Cleanup(func() { newWithConfig = original })

	wantErr := errors.New("boom")
	newWithConfig = func(context.Context, *pgxpool.Config) (*pgxpool.Pool, error) {
		return nil, wantErr
	}

	_, err := NewPool(context.Background(), "postgres://user:pass@localhost:5432/db")
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected wrapped error, got %v", err)
	}
}

func TestHealthCheck(t *testing.T) {
	if err := HealthCheck(context.Background(), &fakePool{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantErr := errors.New("boom")
	err := HealthCheck(context.Background(), &fakePool{pingErr: wantErr})
	if err == nil || !errors.Is(err, wantErr) {
		t.Fatalf("expected wrapped error, got %v", err)
	}
}

func TestWithTx_CommitsOnSuccess(t *testing.T) {
	tx := &fakeTx{}
	pool := &fakePool{tx: tx}

	err := WithTx(context.Background(), pool, func(context.Context, pgx.Tx) error {
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !tx.committed || tx.rolledBack {
		t.Fatalf("expected commit, got committed=%v rolledBack=%v", tx.committed, tx.rolledBack)
	}
}

func TestWithTx_RollsBackOnError(t *testing.T) {
	tx := &fakeTx{}
	pool := &fakePool{tx: tx}
	wantErr := errors.New("fn failed")

	err := WithTx(context.Background(), pool, func(context.Context, pgx.Tx) error {
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}
	if tx.committed || !tx.rolledBack {
		t.Fatalf("expected rollback, got committed=%v rolledBack=%v", tx.committed, tx.rolledBack)
	}
}

func TestWithTx_BeginError(t *testing.T) {
	wantErr := errors.New("begin failed")
	pool := &fakePool{beginErr: wantErr}

	err := WithTx(context.Background(), pool, func(context.Context, pgx.Tx) error {
		t.Fatal("fn should not run")
		return nil
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}
}

func TestWithTx_RollsBackOnPanic(t *testing.T) {
	tx := &fakeTx{}
	pool := &fakePool{tx: tx}

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic to propagate")
		}
		if !tx.rolledBack || tx.committed {
			t.Fatalf("expected rollback, got committed=%v rolledBack=%v", tx.committed, tx.rolledBack)
		}
	}()

	_ = WithTx(context.Background(), pool, func(context.Context, pgx.Tx) error {
		panic("boom")
	})
}

func TestWithTx_CommitError(t *testing.T) {
	commitErr := errors.New("commit failed")
	tx := &fakeTx{commitErr: commitErr}
	pool := &fakePool{tx: tx}

	err := WithTx(context.Background(), pool, func(context.Context, pgx.Tx) error {
		return nil
	})
	if !errors.Is(err, commitErr) {
		t.Fatalf("expected %v, got %v", commitErr, err)
	}
}
