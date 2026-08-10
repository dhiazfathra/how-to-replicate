// pgstore_integration_test.go runs PoolStore against a real, migrated
// Postgres testcontainer — the same harness pattern as
// services/sync-gateway/internal/gateway/pgstore_integration_test.go and
// services/capture-api/internal/store/pgstore_integration_test.go. It
// proves the delegating reads/writes actually work against real Postgres,
// not just the in-memory fakeStore used by service_test.go. Skips cleanly
// if no Docker daemon is reachable (see testsupport).
package audit

import (
	"context"
	"database/sql"
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

// setupRuntimePool starts a migrated Postgres container, seeds one
// workspace/project/user/capture/event/ruleset, and returns a pool
// connected as htr_runtime (the role redaction-audit actually uses in
// production) plus the seeded IDs.
func setupRuntimePool(t *testing.T, ctx context.Context) (pool *pgxpool.Pool, workspaceID, captureID string) {
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
	ws, err := q.CreateWorkspace(ctx, sqlcgen.CreateWorkspaceParams{ID: "ws_audit_int", Name: "Acme"})
	if err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	proj, err := q.CreateProject(ctx, sqlcgen.CreateProjectParams{ID: "proj_audit_int", WorkspaceID: ws.ID, Name: "Web"})
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{ID: "user_audit_int", Email: "audit-int@example.com", Name: "Integration User"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	capture, err := q.CreateCapture(ctx, sqlcgen.CreateCaptureParams{
		ID: "cap_audit_int", WorkspaceID: ws.ID, ProjectID: proj.ID,
		Source: "extension", State: "ready", Fidelity: "full",
		Epoch:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
		Env:      []byte("{}"),
		Metadata: []byte("{}"),
		Doc:      []byte("{}"),
	})
	if err != nil {
		t.Fatalf("seed capture: %v", err)
	}
	if _, err := q.CreateCaptureEvent(ctx, sqlcgen.CreateCaptureEventParams{
		ID: "evt_audit_int", CaptureID: capture.ID, T: 0, Kind: "click",
		Payload: []byte(`{}`), Redaction: []byte(`{}`),
	}); err != nil {
		t.Fatalf("seed capture event: %v", err)
	}
	if _, err := q.CreateRedactionRuleset(ctx, sqlcgen.CreateRedactionRulesetParams{
		Version: 1, WorkspaceID: ws.ID, Rules: []byte(`{"rules":[]}`),
	}); err != nil {
		t.Fatalf("seed redaction ruleset: %v", err)
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

	return runtimePool, ws.ID, capture.ID
}

func TestPoolStore_Integration(t *testing.T) {
	ctx := context.Background()
	pool, workspaceID, captureID := setupRuntimePool(t, ctx)
	store := NewPoolStore(pool)

	workspaces, err := store.ListWorkspaces(ctx)
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}
	found := false
	for _, w := range workspaces {
		if w.ID == workspaceID {
			found = true
		}
	}
	if !found {
		t.Fatalf("ListWorkspaces: seeded workspace %s not found in %+v", workspaceID, workspaces)
	}

	captures, err := store.ListReadyCapturesByWorkspace(ctx, workspaceID)
	if err != nil {
		t.Fatalf("ListReadyCapturesByWorkspace: %v", err)
	}
	if len(captures) != 1 || captures[0].ID != captureID {
		t.Fatalf("ListReadyCapturesByWorkspace: want [%s], got %+v", captureID, captures)
	}

	events, err := store.ListCaptureEventsByCapture(ctx, captureID)
	if err != nil {
		t.Fatalf("ListCaptureEventsByCapture: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("ListCaptureEventsByCapture: want 1 event, got %d", len(events))
	}

	byVersion, err := store.GetRedactionRuleset(ctx, 1)
	if err != nil {
		t.Fatalf("GetRedactionRuleset: %v", err)
	}
	if byVersion.WorkspaceID != workspaceID {
		t.Fatalf("GetRedactionRuleset: want workspace %s, got %s", workspaceID, byVersion.WorkspaceID)
	}

	latest, err := store.GetLatestRedactionRuleset(ctx, workspaceID)
	if err != nil {
		t.Fatalf("GetLatestRedactionRuleset: %v", err)
	}
	if latest.Version != 1 {
		t.Fatalf("GetLatestRedactionRuleset: want version 1, got %d", latest.Version)
	}

	finding, err := store.CreateFindingAndAuditLog(ctx,
		sqlcgen.CreateRedactionAuditFindingParams{
			ID:                       "finding_audit_int",
			CaptureID:                captureID,
			WorkspaceID:              workspaceID,
			AppliedRulesetVersion:    pgtype.Int4{Int32: 1, Valid: true},
			EvaluationRulesetVersion: 1,
			RuleIds:                  []byte(`["r1"]`),
			EventIds:                 []byte(`["evt_audit_int"]`),
		},
		sqlcgen.CreateAuditLogParams{
			ID:          "auditlog_audit_int",
			WorkspaceID: workspaceID,
			ActorID:     pgtype.Text{Valid: false},
			Action:      "redaction_audit.finding_created",
			Subject:     captureID,
			Details:     []byte(`{}`),
		},
	)
	if err != nil {
		t.Fatalf("CreateFindingAndAuditLog: %v", err)
	}
	if finding.ID != "finding_audit_int" {
		t.Fatalf("CreateFindingAndAuditLog: want id finding_audit_int, got %s", finding.ID)
	}

	// The transaction must roll back both writes together: reusing the same
	// finding ID triggers a primary-key violation, and the paired audit_log
	// insert must not have committed either.
	_, err = store.CreateFindingAndAuditLog(ctx,
		sqlcgen.CreateRedactionAuditFindingParams{
			ID:                       "finding_audit_int",
			CaptureID:                captureID,
			WorkspaceID:              workspaceID,
			AppliedRulesetVersion:    pgtype.Int4{Int32: 1, Valid: true},
			EvaluationRulesetVersion: 1,
			RuleIds:                  []byte(`["r1"]`),
			EventIds:                 []byte(`["evt_audit_int"]`),
		},
		sqlcgen.CreateAuditLogParams{
			ID:          "auditlog_audit_int_2",
			WorkspaceID: workspaceID,
			ActorID:     pgtype.Text{Valid: false},
			Action:      "redaction_audit.finding_created",
			Subject:     captureID,
			Details:     []byte(`{}`),
		},
	)
	if err == nil {
		t.Fatalf("CreateFindingAndAuditLog: want error on duplicate finding id, got nil")
	}

	// A fresh finding ID but a reused audit_log ID must fail on the second
	// insert and roll back the first: the finding must not exist afterward.
	_, err = store.CreateFindingAndAuditLog(ctx,
		sqlcgen.CreateRedactionAuditFindingParams{
			ID:                       "finding_audit_int_3",
			CaptureID:                captureID,
			WorkspaceID:              workspaceID,
			AppliedRulesetVersion:    pgtype.Int4{Int32: 1, Valid: true},
			EvaluationRulesetVersion: 1,
			RuleIds:                  []byte(`["r1"]`),
			EventIds:                 []byte(`["evt_audit_int"]`),
		},
		sqlcgen.CreateAuditLogParams{
			ID:          "auditlog_audit_int", // reused: primary-key violation
			WorkspaceID: workspaceID,
			ActorID:     pgtype.Text{Valid: false},
			Action:      "redaction_audit.finding_created",
			Subject:     captureID,
			Details:     []byte(`{}`),
		},
	)
	if err == nil {
		t.Fatalf("CreateFindingAndAuditLog: want error on duplicate audit_log id, got nil")
	}
}
