package migrate

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/dhiazfathra/how-to-replicate/services/internal/testsupport"
)

func TestUpAndStatus(t *testing.T) {
	dsn := testsupport.Postgres(t, context.Background())

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := Up(db); err != nil {
		t.Fatalf("Up: %v", err)
	}

	version, err := Status(db)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if version != 1 {
		t.Fatalf("expected version 1, got %d", version)
	}
}

func TestUpAndStatus_ClosedDB(t *testing.T) {
	db, err := sql.Open("pgx", "postgres://bad")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	_ = db.Close()

	if err := Up(db); err == nil {
		t.Fatal("expected error against closed db")
	}

	if _, err := Status(db); err == nil {
		t.Fatal("expected error against closed db")
	}
}

func TestUpAndStatus_SetDialectError(t *testing.T) {
	original := setDialect
	t.Cleanup(func() { setDialect = original })

	wantErr := errors.New("boom")
	setDialect = func(string) error { return wantErr }

	db, err := sql.Open("pgx", "postgres://bad")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := Up(db); !errors.Is(err, wantErr) {
		t.Fatalf("expected wrapped error, got %v", err)
	}

	if _, err := Status(db); !errors.Is(err, wantErr) {
		t.Fatalf("expected wrapped error, got %v", err)
	}
}
