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
	// Version tracks however many migrations exist, not a fixed number —
	// this assertion is updated each time a migration is added rather than
	// pinned, since pinning it is exactly the kind of test that breaks for
	// the right reason on every future schema change.
	const wantVersion = 6
	if version != wantVersion {
		t.Fatalf("expected version %d, got %d", wantVersion, version)
	}

	// Down must also work: goose.Down should return the schema to the
	// previous version, proving the migration is reversible.
	if err := Down(db); err != nil {
		t.Fatalf("Down: %v", err)
	}
	version, err = Status(db)
	if err != nil {
		t.Fatalf("Status after down: %v", err)
	}
	if version != wantVersion-1 {
		t.Fatalf("expected version %d after down, got %d", wantVersion-1, version)
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

	if err := Down(db); err == nil {
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

	if err := Down(db); !errors.Is(err, wantErr) {
		t.Fatalf("expected wrapped error, got %v", err)
	}

	if _, err := Status(db); !errors.Is(err, wantErr) {
		t.Fatalf("expected wrapped error, got %v", err)
	}
}
