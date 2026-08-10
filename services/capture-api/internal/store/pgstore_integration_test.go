// pgstore_integration_test.go proves Store's queries behave against a
// real, migrated Postgres testcontainer — same harness pattern as
// sync-gateway's pgstore_integration_test.go. Skips cleanly if no Docker
// daemon is reachable (see testsupport).
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/dhiazfathra/how-to-replicate/services/internal/db"
	"github.com/dhiazfathra/how-to-replicate/services/internal/db/sqlcgen"
	"github.com/dhiazfathra/how-to-replicate/services/internal/migrate"
	"github.com/dhiazfathra/how-to-replicate/services/internal/testsupport"
)

func TestStore_Integration(t *testing.T) {
	ctx := context.Background()
	migrationDSN := testsupport.Postgres(t, ctx)

	sqlDB, err := sql.Open("pgx", migrationDSN)
	if err != nil {
		t.Fatalf("open migration db: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	if err := migrate.Up(sqlDB); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	runtimeDSN, err := db.WithRuntimeRole(migrationDSN, testsupport.RuntimeRolePassword)
	if err != nil {
		t.Fatalf("build runtime dsn: %v", err)
	}
	pool, err := pgxpool.New(ctx, runtimeDSN)
	if err != nil {
		t.Fatalf("connect runtime pool: %v", err)
	}
	defer pool.Close()

	s := New(sqlcgen.New(pool))

	if _, err := s.UpsertUser(ctx, "user_int", "int@example.com", "Integration User"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := s.UpsertUser(ctx, "user_stranger", "stranger@example.com", "Stranger"); err != nil {
		t.Fatalf("seed stranger user: %v", err)
	}

	// CreateWorkspaceWithOwner: workspace + policy overrides + owner
	// membership, all in one tx.
	ws, err := CreateWorkspaceWithOwner(ctx, pool, "ws_int", "Acme", "mem_int", "user_int", []byte(`{"retentionDays":90}`))
	if err != nil {
		t.Fatalf("CreateWorkspaceWithOwner: %v", err)
	}
	role, ok, err := s.RoleInWorkspace(ctx, ws.ID, "user_int")
	if err != nil || !ok || role != "owner" {
		t.Fatalf("RoleInWorkspace() = (%q, %v, %v), want (owner, true, nil)", role, ok, err)
	}

	// A duplicate workspace ID fails CreateWorkspace itself (primary key
	// violation), before CreateMembership ever runs — proves the tx is
	// rolled back and the error surfaces rather than a partial write.
	if _, err := CreateWorkspaceWithOwner(ctx, pool, "ws_int", "Acme Duplicate", "mem_int_dup", "user_int", []byte(`{"retentionDays":90}`)); err == nil {
		t.Fatal("CreateWorkspaceWithOwner() with duplicate id: err = nil, want error")
	}
	var membershipCount int
	if err := sqlDB.QueryRowContext(ctx, "SELECT count(*) FROM memberships WHERE id = $1", "mem_int_dup").Scan(&membershipCount); err != nil {
		t.Fatalf("count memberships: %v", err)
	}
	if membershipCount != 0 {
		t.Fatalf("membership mem_int_dup exists after failed CreateWorkspaceWithOwner, want tx rolled back")
	}

	// A different user has no membership row — same not-found shape as a
	// workspace that doesn't exist at all.
	if _, ok, err := s.RoleInWorkspace(ctx, ws.ID, "user_stranger"); err != nil || ok {
		t.Fatalf("RoleInWorkspace() for non-member = (ok=%v, err=%v), want (false, nil)", ok, err)
	}
	if _, err := s.GetWorkspaceForMember(ctx, ws.ID, "user_stranger"); err != ErrNotFound {
		t.Fatalf("GetWorkspaceForMember() for non-member = %v, want ErrNotFound", err)
	}
	if _, err := s.GetWorkspaceForMember(ctx, "ws_does_not_exist", "user_int"); err != ErrNotFound {
		t.Fatalf("GetWorkspaceForMember() for nonexistent workspace = %v, want ErrNotFound", err)
	}

	// Project CRUD, scoped by workspace.
	proj, err := s.CreateProject(ctx, "proj_int", ws.ID, "Web")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := s.GetProject(ctx, proj.ID, "ws_other_workspace"); err != ErrNotFound {
		t.Fatalf("GetProject() cross-workspace = %v, want ErrNotFound", err)
	}
	if got, err := s.GetProject(ctx, proj.ID, ws.ID); err != nil || got.ID != proj.ID {
		t.Fatalf("GetProject() = (%v, %v)", got, err)
	}

	// Policy overrides round-trip.
	if _, err := s.SetPolicyOverrides(ctx, ws.ID, []byte(`{"retentionDays":30}`)); err != nil {
		t.Fatalf("SetPolicyOverrides: %v", err)
	}
	got, err := s.GetWorkspace(ctx, ws.ID)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	var overrides struct {
		RetentionDays int `json:"retentionDays"`
	}
	if err := json.Unmarshal(got.PolicyOverrides, &overrides); err != nil || overrides.RetentionDays != 30 {
		t.Fatalf("PolicyOverrides = %s, want retentionDays:30 (err=%v)", got.PolicyOverrides, err)
	}

	// --- Task 13: capture creation/access audit + the purge state machine ---

	q := sqlcgen.New(pool)
	if _, err := q.CreateProject(ctx, sqlcgen.CreateProjectParams{ID: "proj_purge", WorkspaceID: ws.ID, Name: "Purge"}); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	// CreateCaptureAndAudit: the capture row and its "capture.create"
	// audit_log entry commit together.
	cap1, err := CreateCaptureAndAudit(ctx, pool, "user_int", sqlcgen.CreateCaptureParams{
		ID: "cap_purge", WorkspaceID: ws.ID, ProjectID: "proj_purge",
		Source: "extension", State: "ready", Fidelity: "full",
		Epoch: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		Env:   []byte("{}"), Metadata: []byte("{}"), Doc: []byte(`{"steps":["a"]}`),
	})
	if err != nil {
		t.Fatalf("CreateCaptureAndAudit: %v", err)
	}
	var createAuditCount int
	if err := sqlDB.QueryRowContext(ctx,
		"SELECT count(*) FROM audit_log WHERE action = 'capture.create' AND subject = $1", cap1.ID,
	).Scan(&createAuditCount); err != nil {
		t.Fatalf("count capture.create audit rows: %v", err)
	}
	if createAuditCount != 1 {
		t.Fatalf("capture.create audit rows = %d, want exactly 1", createAuditCount)
	}

	// GetCaptureAndAudit: exactly one "capture.access" entry per call.
	if _, err := s.GetCaptureAndAudit(ctx, cap1.ID, ws.ID, "user_int", "audit_access_1"); err != nil {
		t.Fatalf("GetCaptureAndAudit: %v", err)
	}
	var accessAuditCount int
	if err := sqlDB.QueryRowContext(ctx,
		"SELECT count(*) FROM audit_log WHERE action = 'capture.access' AND subject = $1", cap1.ID,
	).Scan(&accessAuditCount); err != nil {
		t.Fatalf("count capture.access audit rows: %v", err)
	}
	if accessAuditCount != 1 {
		t.Fatalf("capture.access audit rows = %d, want exactly 1", accessAuditCount)
	}

	if _, err := q.CreateAsset(ctx, sqlcgen.CreateAssetParams{
		ID: "asset_purge", CaptureID: cap1.ID, Kind: "video", MimeType: "video/webm", ObjectKey: "captures/cap_purge/video.webm",
	}); err != nil {
		t.Fatalf("seed asset: %v", err)
	}

	// TombstoneCaptureForPurge: the compliance deadline. The capture flips
	// to 'expired' (unreadable via the same ready-gate every read path
	// uses) in the SAME transaction as the purge_jobs row advancing and
	// the audit entry — all three or none.
	if _, err := q.CreatePurgeJobIfAbsent(ctx, sqlcgen.CreatePurgeJobIfAbsentParams{ID: "job_purge", CaptureID: cap1.ID, WorkspaceID: ws.ID}); err != nil {
		t.Fatalf("CreatePurgeJobIfAbsent: %v", err)
	}
	if _, err := TombstoneCaptureForPurge(ctx, pool, "job_purge", cap1.ID, ws.ID); err != nil {
		t.Fatalf("TombstoneCaptureForPurge: %v", err)
	}
	tombstoned, err := s.GetCapture(ctx, cap1.ID, ws.ID)
	if err != nil {
		t.Fatalf("GetCapture after tombstone: %v", err)
	}
	if tombstoned.State != "expired" {
		t.Fatalf("capture state = %q, want expired immediately after tombstoning", tombstoned.State)
	}
	// The blob is untouched by tombstoning alone — reclaim is a later,
	// separate step; the asset row (the blob's pointer) still exists.
	if _, err := q.GetAsset(ctx, "asset_purge"); err != nil {
		t.Fatalf("asset row missing right after tombstone, want it to still exist: %v", err)
	}
	var tombstoneAuditCount int
	if err := sqlDB.QueryRowContext(ctx,
		"SELECT count(*) FROM audit_log WHERE action = 'capture.delete.tombstoned' AND subject = $1", cap1.ID,
	).Scan(&tombstoneAuditCount); err != nil {
		t.Fatalf("count tombstone audit rows: %v", err)
	}
	if tombstoneAuditCount != 1 {
		t.Fatalf("capture.delete.tombstoned audit rows = %d, want exactly 1", tombstoneAuditCount)
	}

	// FinalizePurgeForCapture: asset rows removed, disclosable content
	// columns cleared, purge_jobs advanced to purged, one more audit
	// entry — all in one transaction.
	if _, err := FinalizePurgeForCapture(ctx, pool, "job_purge", cap1.ID, ws.ID); err != nil {
		t.Fatalf("FinalizePurgeForCapture: %v", err)
	}
	if _, err := q.GetAsset(ctx, "asset_purge"); err == nil {
		t.Fatal("asset row survived FinalizePurgeForCapture, want it removed")
	}
	purged, err := s.GetCapture(ctx, cap1.ID, ws.ID)
	if err != nil {
		t.Fatalf("GetCapture after finalize: %v", err)
	}
	if string(purged.Doc) != "{}" {
		t.Fatalf("capture doc = %s, want cleared to {}", purged.Doc)
	}
	job, err := q.GetPurgeJob(ctx, "job_purge")
	if err != nil {
		t.Fatalf("GetPurgeJob: %v", err)
	}
	if job.State != "purged" {
		t.Fatalf("purge job state = %q, want purged", job.State)
	}
	var finalAuditCount int
	if err := sqlDB.QueryRowContext(ctx,
		"SELECT count(*) FROM audit_log WHERE action = 'capture.delete.purged' AND subject = $1", cap1.ID,
	).Scan(&finalAuditCount); err != nil {
		t.Fatalf("count final audit rows: %v", err)
	}
	if finalAuditCount != 1 {
		t.Fatalf("capture.delete.purged audit rows = %d, want exactly 1", finalAuditCount)
	}

	// audit_log rejects UPDATE and DELETE outright (Task 3's trigger),
	// re-confirmed here since this task adds new audit_log write paths.
	if _, err := sqlDB.ExecContext(ctx, "UPDATE audit_log SET action = 'tampered' WHERE subject = $1", cap1.ID); err == nil {
		t.Fatal("expected UPDATE on audit_log to be rejected by the append-only trigger")
	}
	if _, err := sqlDB.ExecContext(ctx, "DELETE FROM audit_log WHERE subject = $1", cap1.ID); err == nil {
		t.Fatal("expected DELETE on audit_log to be rejected by the append-only trigger")
	}
}

// TestPurgePrimitives_ErrorPaths exercises every error-return branch of
// the Task 13 additions that the happy-path test above doesn't reach:
// SetPolicyOverrides failing inside CreateWorkspaceWithOwner, a capture
// row that doesn't exist for both CreateCaptureAndAudit's audit half and
// GetCaptureAndAudit/TombstoneCaptureForPurge/FinalizePurgeForCapture, a
// duplicate audit_log id, and the PurgeTxStore adapter methods (thin
// delegation, otherwise untouched by the pool-free unit tests in
// purge_test.go).
func TestPurgePrimitives_ErrorPaths(t *testing.T) {
	ctx := context.Background()
	migrationDSN := testsupport.Postgres(t, ctx)

	sqlDB, err := sql.Open("pgx", migrationDSN)
	if err != nil {
		t.Fatalf("open migration db: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()
	if err := migrate.Up(sqlDB); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	runtimeDSN, err := db.WithRuntimeRole(migrationDSN, testsupport.RuntimeRolePassword)
	if err != nil {
		t.Fatalf("build runtime dsn: %v", err)
	}
	pool, err := pgxpool.New(ctx, runtimeDSN)
	if err != nil {
		t.Fatalf("connect runtime pool: %v", err)
	}
	defer pool.Close()

	s := New(sqlcgen.New(pool))
	if _, err := s.UpsertUser(ctx, "user_err", "err@example.com", "Err User"); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	// SetPolicyOverrides failing (malformed JSON) inside
	// CreateWorkspaceWithOwner's transaction: the whole workspace must not
	// exist afterward.
	if _, err := CreateWorkspaceWithOwner(ctx, pool, "ws_err", "Err Inc", "mem_err", "user_err", []byte("not-json")); err == nil {
		t.Fatal("CreateWorkspaceWithOwner() with malformed policy overrides: err = nil, want error")
	}
	if _, err := s.GetWorkspace(ctx, "ws_err"); err != ErrNotFound {
		t.Fatalf("GetWorkspace() after failed CreateWorkspaceWithOwner = %v, want ErrNotFound (tx rolled back)", err)
	}

	ws, err := CreateWorkspaceWithOwner(ctx, pool, "ws_err_ok", "Err Inc", "mem_err_ok", "user_err", []byte(`{"retentionDays":90}`))
	if err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	q := sqlcgen.New(pool)
	if _, err := q.CreateProject(ctx, sqlcgen.CreateProjectParams{ID: "proj_err", WorkspaceID: ws.ID, Name: "Err"}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	cap1, err := CreateCaptureAndAudit(ctx, pool, "user_err", sqlcgen.CreateCaptureParams{
		ID: "cap_err_ok", WorkspaceID: ws.ID, ProjectID: "proj_err",
		Source: "extension", State: "ready", Fidelity: "full",
		Epoch: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		Env:   []byte("{}"), Metadata: []byte("{}"), Doc: []byte("{}"),
	})
	if err != nil {
		t.Fatalf("seed capture: %v", err)
	}

	// A capture ID that doesn't exist: CreateCaptureAndAudit's underlying
	// insert fails on the project_id FK before the audit half ever runs.
	if _, err := CreateCaptureAndAudit(ctx, pool, "user_err", sqlcgen.CreateCaptureParams{
		ID: "cap_err", WorkspaceID: "ws_does_not_exist", ProjectID: "proj_does_not_exist",
		Source: "extension", State: "ready", Fidelity: "full",
		Epoch: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}); err == nil {
		t.Fatal("CreateCaptureAndAudit() with nonexistent workspace/project: err = nil, want error")
	}

	// GetCaptureAndAudit on a capture that doesn't exist.
	if _, err := s.GetCaptureAndAudit(ctx, "cap_does_not_exist", ws.ID, "user_err", "audit_missing"); err != ErrNotFound {
		t.Fatalf("GetCaptureAndAudit() for missing capture = %v, want ErrNotFound", err)
	}

	// A duplicate audit_log id: the capture read succeeds but the audit
	// write hits a PK conflict, and GetCaptureAndAudit must surface that
	// rather than silently returning the capture anyway.
	if _, err := q.CreateAuditLog(ctx, sqlcgen.CreateAuditLogParams{
		ID: "audit_dup", WorkspaceID: ws.ID, Action: "capture.access", Subject: cap1.ID, Details: []byte("{}"),
	}); err != nil {
		t.Fatalf("seed duplicate audit id: %v", err)
	}
	if _, err := s.GetCaptureAndAudit(ctx, cap1.ID, ws.ID, "user_err", "audit_dup"); err == nil {
		t.Fatal("GetCaptureAndAudit() with a duplicate audit id: err = nil, want error")
	}

	// TombstoneCaptureForPurge/FinalizePurgeForCapture against a capture
	// that does not exist: both must surface the underlying ErrNoRows
	// wrapped, not silently succeed.
	if _, err := TombstoneCaptureForPurge(ctx, pool, "job_err", "cap_does_not_exist", ws.ID); err == nil {
		t.Fatal("TombstoneCaptureForPurge() for missing capture: err = nil, want error")
	}
	if _, err := FinalizePurgeForCapture(ctx, pool, "job_err", "cap_does_not_exist", ws.ID); err == nil {
		t.Fatal("FinalizePurgeForCapture() for missing capture: err = nil, want error")
	}

	// The capture exists but the purge_jobs row named by jobID does not:
	// TombstoneCaptureForPurge's SetPurgeJobState call fails after the
	// capture-side update already ran, and the whole transaction — capture
	// update included — must roll back.
	if _, err := TombstoneCaptureForPurge(ctx, pool, "job_does_not_exist", cap1.ID, ws.ID); err == nil {
		t.Fatal("TombstoneCaptureForPurge() with missing job row: err = nil, want error")
	}
	stillReady, err := s.GetCapture(ctx, cap1.ID, ws.ID)
	if err != nil {
		t.Fatalf("GetCapture after rolled-back tombstone: %v", err)
	}
	if stillReady.State != "ready" {
		t.Fatalf("capture state = %q, want ready (tombstone tx must have rolled back)", stillReady.State)
	}
	if _, err := FinalizePurgeForCapture(ctx, pool, "job_does_not_exist", cap1.ID, ws.ID); err == nil {
		t.Fatal("FinalizePurgeForCapture() with missing job row: err = nil, want error")
	}

	// PurgeTxStore: thin delegation to the two functions above, exercised
	// directly since purge_test.go's unit tests fake this interface
	// rather than hitting the real adapter.
	txStore := PurgeTxStore{Pool: pool}
	if _, err := txStore.TombstoneCaptureForPurge(ctx, "job_err", "cap_does_not_exist", ws.ID); err == nil {
		t.Fatal("PurgeTxStore.TombstoneCaptureForPurge() for missing capture: err = nil, want error")
	}
	if _, err := txStore.FinalizePurgeForCapture(ctx, "job_err", "cap_does_not_exist", ws.ID); err == nil {
		t.Fatal("PurgeTxStore.FinalizePurgeForCapture() for missing capture: err = nil, want error")
	}

	// And the success path for the two adapter methods, so PurgeTxStore's
	// non-error branch is covered too.
	if _, err := q.CreatePurgeJobIfAbsent(ctx, sqlcgen.CreatePurgeJobIfAbsentParams{ID: "job_err_ok", CaptureID: cap1.ID, WorkspaceID: ws.ID}); err != nil {
		t.Fatalf("seed purge job: %v", err)
	}
	if _, err := txStore.TombstoneCaptureForPurge(ctx, "job_err_ok", cap1.ID, ws.ID); err != nil {
		t.Fatalf("PurgeTxStore.TombstoneCaptureForPurge() = %v, want success", err)
	}
	if _, err := txStore.FinalizePurgeForCapture(ctx, "job_err_ok", cap1.ID, ws.ID); err != nil {
		t.Fatalf("PurgeTxStore.FinalizePurgeForCapture() = %v, want success", err)
	}

	// Re-running FinalizePurgeForCapture for the same job collides on its
	// deterministic "audit_purge_final_"+jobID id — the audit write fails,
	// and that failure must surface rather than be swallowed even though
	// every step before it (delete assets, clear content, advance state)
	// is itself a harmless no-op the second time around.
	if _, err := FinalizePurgeForCapture(ctx, pool, "job_err_ok", cap1.ID, ws.ID); err == nil {
		t.Fatal("FinalizePurgeForCapture() re-run with the same job id: err = nil, want error (duplicate audit_log id)")
	}

	// FinalizePurgeForCapture's very first statement — DeleteAssetsForCapture —
	// only errors on something the query itself is refused, not on zero rows
	// matched. Revoke the runtime role's DELETE on assets via the admin
	// connection so that first statement fails with a permission error, then
	// restore the grant. This is the one deterministic way to exercise that
	// error branch without breaking the transaction wrapper itself.
	if _, err := sqlDB.ExecContext(ctx, "REVOKE DELETE ON assets FROM "+db.RuntimeRole); err != nil { //nolint:gosec // db.RuntimeRole is a package constant, not user input
		t.Fatalf("revoke delete on assets: %v", err)
	}
	if _, err := FinalizePurgeForCapture(ctx, pool, "job_err_ok", cap1.ID, ws.ID); err == nil {
		t.Fatal("FinalizePurgeForCapture() with DELETE on assets revoked: err = nil, want error")
	}
	if _, err := sqlDB.ExecContext(ctx, "GRANT DELETE ON assets TO "+db.RuntimeRole); err != nil { //nolint:gosec // db.RuntimeRole is a package constant, not user input
		t.Fatalf("restore delete grant on assets: %v", err)
	}
}

// TestCreateWorkspaceWithOwner_RequiresRetentionOverride proves the
// "required policy value, no code-level default" requirement holds at the
// persistence layer too, not just in the HTTP handler: overrides with no
// retentionDays (or one that resolves to zero) still create a workspace
// row here (Store has no opinion on policy semantics — see
// internal/policy, which is where Validate lives), but the row's
// PolicyOverrides never silently gains a value nobody chose.
func TestCreateWorkspaceWithOwner_RequiresRetentionOverride(t *testing.T) {
	ctx := context.Background()
	migrationDSN := testsupport.Postgres(t, ctx)

	sqlDB, err := sql.Open("pgx", migrationDSN)
	if err != nil {
		t.Fatalf("open migration db: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()
	if err := migrate.Up(sqlDB); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	runtimeDSN, err := db.WithRuntimeRole(migrationDSN, testsupport.RuntimeRolePassword)
	if err != nil {
		t.Fatalf("build runtime dsn: %v", err)
	}
	pool, err := pgxpool.New(ctx, runtimeDSN)
	if err != nil {
		t.Fatalf("connect runtime pool: %v", err)
	}
	defer pool.Close()

	s := New(sqlcgen.New(pool))
	if _, err := s.UpsertUser(ctx, "user_no_retention", "nr@example.com", "No Retention"); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	ws, err := CreateWorkspaceWithOwner(ctx, pool, "ws_no_retention", "No Retention Inc", "mem_no_retention", "user_no_retention", []byte(`{}`))
	if err != nil {
		t.Fatalf("CreateWorkspaceWithOwner: %v", err)
	}
	if string(ws.PolicyOverrides) != "{}" {
		t.Fatalf("PolicyOverrides = %s, want the exact overrides supplied, untouched", ws.PolicyOverrides)
	}
}
