// pgstore_integration_test.go runs pgStore and Gateway end-to-end against
// a real, migrated Postgres testcontainer — the same harness pattern as
// services/internal/db/sqlcgen/roundtrip_test.go. It proves the FOR UPDATE
// lock, the ON CONFLICT DO NOTHING idempotency insert, and the upsert on
// capture_field_versions actually behave as intended against real
// Postgres, not just against the in-memory fake in gateway_test.go. Skips
// cleanly if no Docker daemon is reachable (see testsupport).
package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"

	syncv1 "github.com/dhiazfathra/how-to-replicate/proto/gen/go/sync/v1"
	"github.com/dhiazfathra/how-to-replicate/services/internal/db"
	"github.com/dhiazfathra/how-to-replicate/services/internal/db/sqlcgen"
	"github.com/dhiazfathra/how-to-replicate/services/internal/migrate"
	"github.com/dhiazfathra/how-to-replicate/services/internal/testsupport"
)

// setupRuntimePool starts a migrated Postgres container, seeds one
// workspace/project/capture, and returns a pool connected as htr_runtime
// (the role sync-gateway actually uses in production).
func setupRuntimePool(t *testing.T, ctx context.Context) (*pgxpool.Pool, string) {
	t.Helper()

	migrationDSN := testsupport.Postgres(t, ctx)

	sqlDB, err := sql.Open("pgx", migrationDSN)
	if err != nil {
		t.Fatalf("open migration db: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	if err := migrate.Up(sqlDB); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	migrationPool, err := pgxpool.New(ctx, migrationDSN)
	if err != nil {
		t.Fatalf("connect migration pool: %v", err)
	}
	defer migrationPool.Close()

	q := sqlcgen.New(migrationPool)
	ws, err := q.CreateWorkspace(ctx, sqlcgen.CreateWorkspaceParams{ID: "ws_int", Name: "Acme"})
	if err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	proj, err := q.CreateProject(ctx, sqlcgen.CreateProjectParams{ID: "proj_int", WorkspaceID: ws.ID, Name: "Web"})
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{ID: "user_int", Email: "int@example.com", Name: "Integration User"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	_, err = q.CreateCapture(ctx, sqlcgen.CreateCaptureParams{
		ID: "cap_int", WorkspaceID: ws.ID, ProjectID: proj.ID,
		Source: "extension", State: "ready", Fidelity: "full",
		Epoch:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
		Env:      []byte("{}"),
		Metadata: []byte("{}"),
		Doc:      []byte("{}"),
	})
	if err != nil {
		t.Fatalf("seed capture: %v", err)
	}

	runtimeDSN, err := db.WithRuntimeRole(migrationDSN, testsupport.RuntimeRolePassword)
	if err != nil {
		t.Fatalf("build runtime dsn: %v", err)
	}
	runtimePool, err := db.NewPool(ctx, runtimeDSN)
	if err != nil {
		t.Fatalf("connect runtime pool: %v", err)
	}
	t.Cleanup(runtimePool.Close)

	return runtimePool, ws.ID
}

func TestPgStore_PushMutations_Integration(t *testing.T) {
	ctx := context.Background()
	pool, workspaceID := setupRuntimePool(t, ctx)

	gw := &Gateway{
		WithinTx: NewTxRunner(pool),
		Now:      func() time.Time { return time.Unix(1700000000, 0) },
	}

	batch := []*syncv1.Mutation{
		{Id: "mi1", CaptureId: "cap_int", Op: &syncv1.Mutation_SetTitle{SetTitle: &syncv1.SetTitle{Title: "Checkout fails"}}},
		{Id: "mi2", CaptureId: "cap_int", Op: &syncv1.Mutation_AppendComment{AppendComment: &syncv1.AppendComment{CommentId: "cmi1", Body: "seen on Safari"}}},
	}

	results, err := gw.PushMutations(ctx, workspaceID, "user_int", batch)
	if err != nil {
		t.Fatalf("PushMutations: %v", err)
	}
	for _, r := range results {
		if !r.Applied {
			t.Fatalf("want applied, got %+v", r)
		}
	}

	// Replay the exact same batch: idempotency must hold against real
	// Postgres (ON CONFLICT DO NOTHING), not just the in-memory fake.
	replay, err := gw.PushMutations(ctx, workspaceID, "user_int", batch)
	if err != nil {
		t.Fatalf("replay PushMutations: %v", err)
	}
	if len(replay) != len(results) {
		t.Fatalf("replay result count mismatch")
	}

	q := sqlcgen.New(pool)
	capture, err := q.GetCapture(ctx, "cap_int")
	if err != nil {
		t.Fatalf("get capture: %v", err)
	}
	if capture.Revision != 2 {
		t.Fatalf("want revision 2 after two accepted mutations (not 4 after replay), got %d", capture.Revision)
	}

	var doc map[string]any
	if err := json.Unmarshal(capture.Doc, &doc); err != nil {
		t.Fatalf("unmarshal doc: %v", err)
	}
	if doc[fieldTitle] != "Checkout fails" {
		t.Fatalf("title not persisted: %+v", doc)
	}

	// A second, later mutation on the same field must win and be visible
	// through the real capture_field_versions upsert.
	later := []*syncv1.Mutation{
		{Id: "mi3", CaptureId: "cap_int", Op: &syncv1.Mutation_SetTitle{SetTitle: &syncv1.SetTitle{Title: "Checkout fails on Safari"}}},
	}
	gwLater := &Gateway{WithinTx: NewTxRunner(pool), Now: func() time.Time { return time.Unix(1700000100, 0) }}
	if _, err := gwLater.PushMutations(ctx, workspaceID, "", later); err != nil {
		t.Fatalf("later PushMutations: %v", err)
	}
	capture, err = q.GetCapture(ctx, "cap_int")
	if err != nil {
		t.Fatalf("get capture: %v", err)
	}
	if err := json.Unmarshal(capture.Doc, &doc); err != nil {
		t.Fatalf("unmarshal doc: %v", err)
	}
	if doc[fieldTitle] != "Checkout fails on Safari" {
		t.Fatalf("later title should have won: %+v", doc)
	}

	// A mutation with an earlier server-received timestamp (e.g.
	// redelivered) must lose against real Postgres too: still accepted
	// (revision advances) but the doc keeps the winner's title, and the
	// loss is recorded to audit_log (RecordSupersededMutation).
	stale := []*syncv1.Mutation{
		{Id: "mi5", CaptureId: "cap_int", Op: &syncv1.Mutation_SetTitle{SetTitle: &syncv1.SetTitle{Title: "Stale title"}}},
	}
	gwStale := &Gateway{WithinTx: NewTxRunner(pool), Now: func() time.Time { return time.Unix(1700000000, 0) }}
	staleResults, err := gwStale.PushMutations(ctx, workspaceID, "", stale)
	if err != nil {
		t.Fatalf("stale PushMutations: %v", err)
	}
	if !staleResults[0].Applied {
		t.Fatalf("losing mutation should still be applied, got %+v", staleResults[0])
	}
	capture, err = q.GetCapture(ctx, "cap_int")
	if err != nil {
		t.Fatalf("get capture: %v", err)
	}
	if err := json.Unmarshal(capture.Doc, &doc); err != nil {
		t.Fatalf("unmarshal doc: %v", err)
	}
	if doc[fieldTitle] != "Checkout fails on Safari" {
		t.Fatalf("stale mutation must not overwrite the winning title: %+v", doc)
	}
	auditLog, err := q.GetAuditLog(ctx, "audit_mi5")
	if err != nil {
		t.Fatalf("want audit log entry for superseded mutation mi5: %v", err)
	}
	if auditLog.Action != "mutation_superseded" {
		t.Fatalf("want mutation_superseded action, got %q", auditLog.Action)
	}

	// Cross-workspace and unknown-capture mutations must both be rejected
	// with the same non-disclosing reason against a real database too.
	cross, err := gw.PushMutations(ctx, "some_other_workspace", "", []*syncv1.Mutation{
		{Id: "mi4", CaptureId: "cap_int", Op: &syncv1.Mutation_SetTitle{SetTitle: &syncv1.SetTitle{Title: "x"}}},
	})
	if err != nil {
		t.Fatalf("cross-workspace PushMutations: %v", err)
	}
	if cross[0].Applied || cross[0].Reason != ReasonNotFound {
		t.Fatalf("want not_found, got %+v", cross[0])
	}
}

// TestPgStore_ErrorWrapping drives pgStore's methods directly (not through
// Gateway) with inputs that make the underlying query fail for reasons
// other than "no rows" — a foreign-key violation, or an already-cancelled
// context — to prove every real-error branch wraps and returns, rather
// than panicking or silently succeeding.
func TestPgStore_ErrorWrapping(t *testing.T) {
	ctx := context.Background()
	pool, workspaceID := setupRuntimePool(t, ctx)
	store := &pgStore{q: sqlcgen.New(pool)}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()

	t.Run("LockCaptureForWorkspace on a cancelled context", func(t *testing.T) {
		if _, _, err := store.LockCaptureForWorkspace(cancelled, "cap_int", workspaceID); err == nil {
			t.Fatalf("want error, got nil")
		}
	})

	t.Run("InsertMutationIfNew against a nonexistent capture", func(t *testing.T) {
		if _, err := store.InsertMutationIfNew(ctx, "mx1", "does_not_exist", "set_title", []byte(`"x"`), 0); err == nil {
			t.Fatalf("want FK-violation error, got nil")
		}
	})

	t.Run("FieldVersion on a cancelled context", func(t *testing.T) {
		if _, _, err := store.FieldVersion(cancelled, "cap_int", fieldTitle); err == nil {
			t.Fatalf("want error, got nil")
		}
	})

	t.Run("SetFieldVersion against a nonexistent capture", func(t *testing.T) {
		v := FieldVersion{ServerT: time.Now(), Revision: 1, MutationID: "mx2"}
		if err := store.SetFieldVersion(ctx, "does_not_exist", fieldTitle, v); err == nil {
			t.Fatalf("want FK-violation error, got nil")
		}
	})

	t.Run("UpdateCaptureRevisionAndDoc against a nonexistent capture", func(t *testing.T) {
		if err := store.UpdateCaptureRevisionAndDoc(ctx, "does_not_exist", 1, []byte("{}")); err == nil {
			t.Fatalf("want no-rows error, got nil")
		}
	})

	t.Run("InsertComment against a nonexistent author", func(t *testing.T) {
		if err := store.InsertComment(ctx, "cmx1", "cap_int", "does_not_exist", "x"); err == nil {
			t.Fatalf("want FK-violation error, got nil")
		}
	})

	t.Run("RecordSupersededMutation against a nonexistent workspace", func(t *testing.T) {
		if err := store.RecordSupersededMutation(ctx, "does_not_exist", "cap_int", "mx3", fieldTitle); err == nil {
			t.Fatalf("want FK-violation error, got nil")
		}
	})
}
