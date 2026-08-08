package gateway

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/dhiazfathra/how-to-replicate/services/internal/db/sqlcgen"
)

// fakeDBTX is a minimal sqlcgen.DBTX that lets a single query fail without
// a real Postgres connection — used only for the one ManifestComplete
// branch (CountUnverifiedAssetsForCapture erroring after
// CountAssetsForCapture already succeeded) that a testcontainer can't
// deterministically reach, since both counts run against the same
// connection over the same request.
type fakeDBTX struct {
	failSQLContains string
	failErr         error
}

func (f *fakeDBTX) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (f *fakeDBTX) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("fakeDBTX: Query not supported")
}

func (f *fakeDBTX) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	if f.failSQLContains != "" && strings.Contains(sql, f.failSQLContains) {
		return errRow{err: f.failErr}
	}
	return countRow{count: 1}
}

// countRow scans a fixed count of 1 into the destination, matching a
// "found, non-zero" result for either asset-count query.
type countRow struct{ count int64 }

func (r countRow) Scan(dest ...any) error {
	*(dest[0].(*int64)) = r.count
	return nil
}

// errRow always fails to scan, simulating a query-level error.
type errRow struct{ err error }

func (r errRow) Scan(...any) error { return r.err }

// TestManifestComplete_UnverifiedCountQueryError covers the branch where
// CountAssetsForCapture succeeds (total > 0) but the subsequent
// CountUnverifiedAssetsForCapture call errors — pgstore.go's
// ManifestComplete must propagate that error rather than treating it as
// "complete" or "incomplete".
func TestManifestComplete_UnverifiedCountQueryError(t *testing.T) {
	boom := errors.New("boom")
	store := &pgStore{q: sqlcgen.New(&fakeDBTX{
		failSQLContains: "verified_at IS NULL",
		failErr:         boom,
	})}

	_, err := store.ManifestComplete(context.Background(), "cap_1")
	if !errors.Is(err, boom) {
		t.Fatalf("want wrapped boom error, got %v", err)
	}
}
