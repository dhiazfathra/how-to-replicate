// service_readonly_test.go proves invariant 7 (CLAUDE.md) against a real,
// migrated Postgres — not just an absence of update-call sites in this
// package's source. It hashes every byte of every captures/capture_events
// row before running the audit and again after, over a workspace that
// contains a genuine PHI leak (so the audit's write path — finding +
// audit_log + alert — actually executes), and requires the hashes to be
// identical. Skips cleanly if no Docker daemon is reachable (see
// testsupport), the same pattern as
// services/sync-gateway/internal/gateway/pgstore_integration_test.go.
package audit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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

// captureDataDigest hashes every column of every row in captures and
// capture_events, in a stable order, so any byte-level change to capture
// data — not just a changed row count — would flip the digest.
func captureDataDigest(t *testing.T, ctx context.Context, q *sqlcgen.Queries, captureIDs []string) string {
	t.Helper()
	h := sha256.New()
	for _, id := range captureIDs {
		capture, err := q.GetCapture(ctx, id)
		if err != nil {
			t.Fatalf("get capture %s: %v", id, err)
		}
		b, _ := json.Marshal(capture)
		h.Write(b)

		events, err := q.ListCaptureEventsByCapture(ctx, id)
		if err != nil {
			t.Fatalf("list events for %s: %v", id, err)
		}
		for _, e := range events {
			eb, _ := json.Marshal(e)
			h.Write(eb)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestService_RunWorkspace_NeverWritesCaptureData_Integration(t *testing.T) {
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

	migrationPool, err := pgxpool.New(ctx, migrationDSN)
	if err != nil {
		t.Fatalf("connect migration pool: %v", err)
	}
	defer migrationPool.Close()
	q := sqlcgen.New(migrationPool)

	ws, err := q.CreateWorkspace(ctx, sqlcgen.CreateWorkspaceParams{ID: "ws_ro", Name: "Acme"})
	if err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	proj, err := q.CreateProject(ctx, sqlcgen.CreateProjectParams{ID: "proj_ro", WorkspaceID: ws.ID, Name: "Web"})
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := q.CreateRedactionRuleset(ctx, sqlcgen.CreateRedactionRulesetParams{
		Version: 1, WorkspaceID: ws.ID, Rules: rulesetBytes(t, 1, nikRule()),
	}); err != nil {
		t.Fatalf("seed ruleset: %v", err)
	}
	if _, err := q.CreateCapture(ctx, sqlcgen.CreateCaptureParams{
		ID: "cap_ro", WorkspaceID: ws.ID, ProjectID: proj.ID,
		Source: "extension", State: "ready", Fidelity: "full",
		Epoch:                 pgtype.Timestamptz{Time: time.Now(), Valid: true},
		Env:                   []byte("{}"),
		Metadata:              []byte("{}"),
		Doc:                   []byte("{}"),
		AppliedRulesetVersion: int4(1),
	}); err != nil {
		t.Fatalf("seed capture: %v", err)
	}
	// A genuine PHI leak: a NIK that got past client-side redaction and
	// synced anyway. This is exactly the case the audit exists to catch —
	// and exactly the case where a filter-shaped bug (mutating the leak
	// away) would be most tempting to write.
	if _, err := q.CreateCaptureEvent(ctx, sqlcgen.CreateCaptureEventParams{
		ID: "evt_ro", CaptureID: "cap_ro", T: 0, Kind: "network",
		Payload:   networkEventPayload(`{"note":"1234567890123456"}`),
		Redaction: []byte("{}"),
	}); err != nil {
		t.Fatalf("seed capture event: %v", err)
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
	runtimeQ := sqlcgen.New(runtimePool)

	before := captureDataDigest(t, ctx, q, []string{"cap_ro"})

	svc := New(runtimeQ, &fakeSink{})
	findings, err := svc.RunWorkspace(ctx, ws.ID, 0)
	if err != nil {
		t.Fatalf("RunWorkspace: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected the seeded leak to be found, got %d findings", len(findings))
	}

	after := captureDataDigest(t, ctx, q, []string{"cap_ro"})
	if before != after {
		t.Fatalf("capture data changed by running the audit: before=%s after=%s", before, after)
	}

	// capture_events is append-only at the database level (00002_schema.sql
	// revokes UPDATE/DELETE from htr_runtime) — this is the belt to the
	// digest's suspenders for events specifically: even a bug that tried to
	// mutate an event would fail at the database, not silently succeed.
	// captures itself is mutable at the DB level (sync-gateway updates
	// revision/doc there), so the digest above — not a grant — is what
	// proves THIS service never writes to it.
	if _, err := runtimePool.Exec(ctx, `UPDATE capture_events SET kind = 'x' WHERE id = $1`, "evt_ro"); err == nil {
		t.Fatal("expected runtime role to be denied UPDATE on capture_events")
	}

	// Findings and audit_log rows, by contrast, DID get written — the
	// audit's only legitimate writes.
	auditFindings, err := q.ListRedactionAuditFindingsByCapture(ctx, "cap_ro")
	if err != nil {
		t.Fatalf("list findings: %v", err)
	}
	if len(auditFindings) != 1 {
		t.Fatalf("expected 1 finding row, got %d", len(auditFindings))
	}
	if auditFindings[0].EvaluationRulesetVersion != 1 || !auditFindings[0].AppliedRulesetVersion.Valid || auditFindings[0].AppliedRulesetVersion.Int32 != 1 {
		t.Fatalf("finding missing version pair: %+v", auditFindings[0])
	}
}
