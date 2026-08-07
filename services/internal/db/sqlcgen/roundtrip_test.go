// Package sqlcgen round-trip and immutability tests run against a real,
// migrated Postgres testcontainer. They prove three things the schema
// design depends on: every generated query round-trips through the real
// database, the append-only tables reject UPDATE/DELETE for the runtime
// role (the grant), and the same statements are rejected for the migration
// role too (the trigger — defence in depth against the owner).
package sqlcgen

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/dhiazfathra/how-to-replicate/services/internal/db"
	"github.com/dhiazfathra/how-to-replicate/services/internal/migrate"
	"github.com/dhiazfathra/how-to-replicate/services/internal/testsupport"
)

var (
	errQueryBoom = errors.New("query boom")
	errScanBoom  = errors.New("scan boom")
	errRowsBoom  = errors.New("rows boom")
)

// postgresDSNs holds the two connection strings these tests need against a
// migrated database: migration role (schema owner, used by goose and to
// prove the trigger also catches the owner) and runtime role (every
// service's actual connection, granted SELECT/INSERT only on the
// append-only tables).
type postgresDSNs struct {
	migration string
	runtime   string
}

// migratedPostgres starts a Postgres testcontainer, applies every
// migration (including the htr_runtime role and grants from
// 00002_schema.sql), and returns DSNs for both roles.
func migratedPostgres(t *testing.T, ctx context.Context) postgresDSNs {
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

	runtimeDSN, err := db.WithRuntimeRole(migrationDSN, testsupport.RuntimeRolePassword)
	if err != nil {
		t.Fatalf("build runtime dsn: %v", err)
	}

	return postgresDSNs{migration: migrationDSN, runtime: runtimeDSN}
}

func mustConnect(t *testing.T, ctx context.Context, dsn string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seed inserts one row in every referenced-first table so the FK-heavy
// tables below (captures, capture_events, ...) have something to point at.
type seed struct {
	workspaceID string
	projectID   string
	userID      string
	rulesetVer  int32
	captureID   string
}

func seedFixtures(t *testing.T, ctx context.Context, q *Queries) seed {
	t.Helper()

	ws, err := q.CreateWorkspace(ctx, CreateWorkspaceParams{ID: "ws_1", Name: "Acme"})
	if err != nil {
		t.Fatalf("seed workspace: %v", err)
	}

	proj, err := q.CreateProject(ctx, CreateProjectParams{ID: "proj_1", WorkspaceID: ws.ID, Name: "Web"})
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}

	user, err := q.CreateUser(ctx, CreateUserParams{ID: "user_1", Email: "a@example.com", Name: "Ada"})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	ruleset, err := q.CreateRedactionRuleset(ctx, CreateRedactionRulesetParams{
		Version: 1, WorkspaceID: ws.ID, Rules: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("seed ruleset: %v", err)
	}

	capture, err := q.CreateCapture(ctx, CreateCaptureParams{
		ID:                    "cap_1",
		WorkspaceID:           ws.ID,
		ProjectID:             proj.ID,
		Source:                "extension",
		State:                 "ready",
		Fidelity:              "full",
		Epoch:                 pgtype.Timestamptz{Valid: true},
		Env:                   []byte(`{}`),
		Metadata:              []byte(`{}`),
		Doc:                   []byte(`{}`),
		WithheldEventCount:    0,
		Revision:              1,
		ManifestComplete:      true,
		AppliedRulesetVersion: pgtype.Int4{Int32: ruleset.Version, Valid: true},
	})
	if err != nil {
		t.Fatalf("seed capture: %v", err)
	}

	return seed{workspaceID: ws.ID, projectID: proj.ID, userID: user.ID, rulesetVer: ruleset.Version, captureID: capture.ID}
}

func TestRoundTrip_EveryTable(t *testing.T) {
	ctx := context.Background()
	dsns := migratedPostgres(t, ctx)

	pool := mustConnect(t, ctx, dsns.migration)
	q := New(pool)

	s := seedFixtures(t, ctx, q)

	if got, err := q.GetWorkspace(ctx, s.workspaceID); err != nil || got.ID != s.workspaceID {
		t.Fatalf("GetWorkspace: %v, %+v", err, got)
	}
	if got, err := q.GetProject(ctx, s.projectID); err != nil || got.ID != s.projectID {
		t.Fatalf("GetProject: %v, %+v", err, got)
	}
	if got, err := q.GetUser(ctx, s.userID); err != nil || got.ID != s.userID {
		t.Fatalf("GetUser: %v, %+v", err, got)
	}
	if got, err := q.GetRedactionRuleset(ctx, s.rulesetVer); err != nil || got.Version != s.rulesetVer {
		t.Fatalf("GetRedactionRuleset: %v, %+v", err, got)
	}
	if got, err := q.GetCapture(ctx, s.captureID); err != nil || got.ID != s.captureID {
		t.Fatalf("GetCapture: %v, %+v", err, got)
	}

	membership, err := q.CreateMembership(ctx, CreateMembershipParams{
		ID: "mem_1", WorkspaceID: s.workspaceID, UserID: s.userID, Role: "owner",
	})
	if err != nil {
		t.Fatalf("CreateMembership: %v", err)
	}
	if got, err := q.GetMembership(ctx, membership.ID); err != nil || got.ID != membership.ID {
		t.Fatalf("GetMembership: %v, %+v", err, got)
	}

	event, err := q.CreateCaptureEvent(ctx, CreateCaptureEventParams{
		ID: "evt_1", CaptureID: s.captureID, T: 1500, Kind: "click", Payload: []byte(`{}`), Redaction: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("CreateCaptureEvent: %v", err)
	}
	if got, err := q.GetCaptureEvent(ctx, event.ID); err != nil || got.ID != event.ID {
		t.Fatalf("GetCaptureEvent: %v, %+v", err, got)
	}
	events, err := q.ListCaptureEventsByCapture(ctx, s.captureID)
	if err != nil || len(events) != 1 {
		t.Fatalf("ListCaptureEventsByCapture: %v, %+v", err, events)
	}

	asset, err := q.CreateAsset(ctx, CreateAssetParams{
		ID: "asset_1", CaptureID: s.captureID, Kind: "video", MimeType: "video/webm",
		SizeBytes: 1024, ChunkCount: 1, Sha256: pgtype.Text{String: "deadbeef", Valid: true}, ObjectKey: "captures/cap_1/video",
	})
	if err != nil {
		t.Fatalf("CreateAsset: %v", err)
	}
	if got, err := q.GetAsset(ctx, asset.ID); err != nil || got.ID != asset.ID {
		t.Fatalf("GetAsset: %v, %+v", err, got)
	}

	mutation, err := q.CreateMutation(ctx, CreateMutationParams{
		ID: "mut_1", CaptureID: s.captureID, Op: "set_title", Payload: []byte(`{"title":"x"}`), ClientT: 42,
	})
	if err != nil {
		t.Fatalf("CreateMutation: %v", err)
	}
	if got, err := q.GetMutation(ctx, mutation.ID); err != nil || got.ID != mutation.ID {
		t.Fatalf("GetMutation: %v, %+v", err, got)
	}
	// Replaying the same client-minted mutation ID is a PK violation, not a
	// silent overwrite — mutations is append-only.
	if _, err := q.CreateMutation(ctx, CreateMutationParams{
		ID: "mut_1", CaptureID: s.captureID, Op: "set_title", Payload: []byte(`{}`), ClientT: 43,
	}); err == nil {
		t.Fatal("expected duplicate mutation id to fail insert")
	}

	// InsertMutationIfNew (Task 4): first insert succeeds, replay is a
	// silent no-op (ErrNoRows from the ON CONFLICT DO NOTHING), not an
	// error — the idempotency contract sync-gateway depends on.
	if _, err := q.InsertMutationIfNew(ctx, InsertMutationIfNewParams{
		ID: "mut_idem", CaptureID: s.captureID, Op: "set_title", Payload: []byte(`{"title":"x"}`), ClientT: 1,
	}); err != nil {
		t.Fatalf("InsertMutationIfNew: %v", err)
	}
	if _, err := q.InsertMutationIfNew(ctx, InsertMutationIfNewParams{
		ID: "mut_idem", CaptureID: s.captureID, Op: "set_title", Payload: []byte(`{"title":"y"}`), ClientT: 2,
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("InsertMutationIfNew replay: want ErrNoRows, got %v", err)
	}

	// LockCaptureForWorkspace (Task 4): matches by (id, workspace_id)
	// together — wrong workspace looks identical to no such capture.
	if got, err := q.LockCaptureForWorkspace(ctx, LockCaptureForWorkspaceParams{
		ID: s.captureID, WorkspaceID: s.workspaceID,
	}); err != nil || got.ID != s.captureID {
		t.Fatalf("LockCaptureForWorkspace: %v, %+v", err, got)
	}
	if _, err := q.LockCaptureForWorkspace(ctx, LockCaptureForWorkspaceParams{
		ID: s.captureID, WorkspaceID: "some_other_workspace",
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("LockCaptureForWorkspace wrong workspace: want ErrNoRows, got %v", err)
	}

	// UpdateCaptureRevisionAndDoc (Task 4): revision and doc both persist.
	updated, err := q.UpdateCaptureRevisionAndDoc(ctx, UpdateCaptureRevisionAndDocParams{
		ID: s.captureID, Revision: 1, Doc: []byte(`{"title":"x"}`),
	})
	if err != nil || updated.Revision != 1 {
		t.Fatalf("UpdateCaptureRevisionAndDoc: %v, %+v", err, updated)
	}

	// GetCaptureFieldVersion / UpsertCaptureFieldVersion (Task 4): no
	// version until the first upsert; a second upsert on the same
	// (capture, field) overwrites in place rather than erroring.
	if _, err := q.GetCaptureFieldVersion(ctx, GetCaptureFieldVersionParams{
		CaptureID: s.captureID, Field: "title",
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("GetCaptureFieldVersion before any write: want ErrNoRows, got %v", err)
	}
	fv, err := q.UpsertCaptureFieldVersion(ctx, UpsertCaptureFieldVersionParams{
		CaptureID: s.captureID, Field: "title",
		ServerT: pgtype.Timestamptz{Time: time.Unix(1000, 0), Valid: true},
		Revision: 1, MutationID: "mut_idem",
	})
	if err != nil || fv.MutationID != "mut_idem" {
		t.Fatalf("UpsertCaptureFieldVersion (insert): %v, %+v", err, fv)
	}
	fv, err = q.UpsertCaptureFieldVersion(ctx, UpsertCaptureFieldVersionParams{
		CaptureID: s.captureID, Field: "title",
		ServerT: pgtype.Timestamptz{Time: time.Unix(2000, 0), Valid: true},
		Revision: 2, MutationID: "mut_1",
	})
	if err != nil || fv.MutationID != "mut_1" || fv.Revision != 2 {
		t.Fatalf("UpsertCaptureFieldVersion (update): %v, %+v", err, fv)
	}
	if got, err := q.GetCaptureFieldVersion(ctx, GetCaptureFieldVersionParams{
		CaptureID: s.captureID, Field: "title",
	}); err != nil || got.MutationID != "mut_1" {
		t.Fatalf("GetCaptureFieldVersion after upsert: %v, %+v", err, got)
	}

	comment, err := q.CreateComment(ctx, CreateCommentParams{
		ID: "cmt_1", CaptureID: s.captureID, AuthorID: s.userID, Body: "hi",
	})
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	if got, err := q.GetComment(ctx, comment.ID); err != nil || got.ID != comment.ID {
		t.Fatalf("GetComment: %v, %+v", err, got)
	}

	audit, err := q.CreateAuditLog(ctx, CreateAuditLogParams{
		ID: "audit_1", WorkspaceID: s.workspaceID, ActorID: pgtype.Text{String: s.userID, Valid: true},
		Action: "capture.viewed", Subject: s.captureID, Details: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("CreateAuditLog: %v", err)
	}
	if got, err := q.GetAuditLog(ctx, audit.ID); err != nil || got.ID != audit.ID {
		t.Fatalf("GetAuditLog: %v, %+v", err, got)
	}

	share, err := q.CreateShareLink(ctx, CreateShareLinkParams{
		ID: "share_1", CaptureID: s.captureID, Token: "tok123", CreatedBy: s.userID,
		ExpiresAt: pgtype.Timestamptz{Valid: false},
	})
	if err != nil {
		t.Fatalf("CreateShareLink: %v", err)
	}
	if got, err := q.GetShareLink(ctx, share.ID); err != nil || got.ID != share.ID {
		t.Fatalf("GetShareLink: %v, %+v", err, got)
	}
	revoked, err := q.RevokeShareLink(ctx, share.ID)
	if err != nil || !revoked.RevokedAt.Valid {
		t.Fatalf("RevokeShareLink: %v, %+v", err, revoked)
	}

	binding, err := q.CreateIntegrationBinding(ctx, CreateIntegrationBindingParams{
		ID: "bind_1", WorkspaceID: s.workspaceID, Provider: "slack", Config: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("CreateIntegrationBinding: %v", err)
	}
	if got, err := q.GetIntegrationBinding(ctx, binding.ID); err != nil || got.ID != binding.ID {
		t.Fatalf("GetIntegrationBinding: %v, %+v", err, got)
	}

	outboxEntry, err := q.CreateOutboxEntry(ctx, CreateOutboxEntryParams{
		ID: "outbox_1", Topic: "capture.ready", Payload: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("CreateOutboxEntry: %v", err)
	}
	if got, err := q.GetOutboxEntry(ctx, outboxEntry.ID); err != nil || got.ID != outboxEntry.ID {
		t.Fatalf("GetOutboxEntry: %v, %+v", err, got)
	}
}

// assertRejected runs stmt against dsn as a plain database/sql connection
// and fails the test unless Postgres rejects it.
func assertRejected(t *testing.T, ctx context.Context, dsn, stmt, label string) {
	t.Helper()

	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("%s: open: %v", label, err)
	}
	defer func() { _ = sqlDB.Close() }()

	if _, err := sqlDB.ExecContext(ctx, stmt); err == nil {
		t.Fatalf("%s: expected %q to be rejected, it succeeded", label, stmt)
	}
}

func TestImmutability_CaptureEventsRejectsUpdateAndDelete(t *testing.T) {
	ctx := context.Background()
	dsns := migratedPostgres(t, ctx)

	pool := mustConnect(t, ctx, dsns.migration)
	q := New(pool)
	s := seedFixtures(t, ctx, q)

	if _, err := q.CreateCaptureEvent(ctx, CreateCaptureEventParams{
		ID: "evt_immutable", CaptureID: s.captureID, T: 1, Kind: "click", Payload: []byte(`{}`), Redaction: []byte(`{}`),
	}); err != nil {
		t.Fatalf("seed capture_events row: %v", err)
	}

	update := "UPDATE capture_events SET kind = 'tampered' WHERE id = 'evt_immutable'"
	del := "DELETE FROM capture_events WHERE id = 'evt_immutable'"

	// As the runtime role: this is the only connection any service ever
	// makes, and the grant alone should stop both statements.
	assertRejected(t, ctx, dsns.runtime, update, "runtime role UPDATE")
	assertRejected(t, ctx, dsns.runtime, del, "runtime role DELETE")

	// As the migration role (schema owner): grants don't bind the owner,
	// so only the trigger can stop this. Proves the trigger, not just the
	// grant, is doing the work.
	assertRejected(t, ctx, dsns.migration, update, "migration role UPDATE")
	assertRejected(t, ctx, dsns.migration, del, "migration role DELETE")
}

func TestImmutability_AuditLogRejectsUpdateAndDelete(t *testing.T) {
	ctx := context.Background()
	dsns := migratedPostgres(t, ctx)

	pool := mustConnect(t, ctx, dsns.migration)
	q := New(pool)
	s := seedFixtures(t, ctx, q)

	if _, err := q.CreateAuditLog(ctx, CreateAuditLogParams{
		ID: "audit_immutable", WorkspaceID: s.workspaceID, ActorID: pgtype.Text{String: s.userID, Valid: true},
		Action: "capture.viewed", Subject: s.captureID, Details: []byte(`{}`),
	}); err != nil {
		t.Fatalf("seed audit_log row: %v", err)
	}

	update := "UPDATE audit_log SET action = 'tampered' WHERE id = 'audit_immutable'"
	del := "DELETE FROM audit_log WHERE id = 'audit_immutable'"

	assertRejected(t, ctx, dsns.runtime, update, "runtime role UPDATE")
	assertRejected(t, ctx, dsns.runtime, del, "runtime role DELETE")
	assertRejected(t, ctx, dsns.migration, update, "migration role UPDATE")
	assertRejected(t, ctx, dsns.migration, del, "migration role DELETE")
}

func TestImmutability_MutationsAndCommentsRejectUpdateAndDelete(t *testing.T) {
	ctx := context.Background()
	dsns := migratedPostgres(t, ctx)

	pool := mustConnect(t, ctx, dsns.migration)
	q := New(pool)
	s := seedFixtures(t, ctx, q)

	if _, err := q.CreateMutation(ctx, CreateMutationParams{
		ID: "mut_immutable", CaptureID: s.captureID, Op: "set_title", Payload: []byte(`{}`), ClientT: 1,
	}); err != nil {
		t.Fatalf("seed mutations row: %v", err)
	}
	if _, err := q.CreateComment(ctx, CreateCommentParams{
		ID: "cmt_immutable", CaptureID: s.captureID, AuthorID: s.userID, Body: "hi",
	}); err != nil {
		t.Fatalf("seed comments row: %v", err)
	}

	for _, tc := range []struct {
		name        string
		update, del string
	}{
		{"mutations", "UPDATE mutations SET op = 'tampered' WHERE id = 'mut_immutable'", "DELETE FROM mutations WHERE id = 'mut_immutable'"},
		{"comments", "UPDATE comments SET body = 'tampered' WHERE id = 'cmt_immutable'", "DELETE FROM comments WHERE id = 'cmt_immutable'"},
	} {
		assertRejected(t, ctx, dsns.runtime, tc.update, tc.name+" runtime role UPDATE")
		assertRejected(t, ctx, dsns.runtime, tc.del, tc.name+" runtime role DELETE")
		assertRejected(t, ctx, dsns.migration, tc.update, tc.name+" migration role UPDATE")
		assertRejected(t, ctx, dsns.migration, tc.del, tc.name+" migration role DELETE")
	}
}

func TestImmutability_RedactionRulesetsRejectsUpdateAndDelete(t *testing.T) {
	ctx := context.Background()
	dsns := migratedPostgres(t, ctx)

	pool := mustConnect(t, ctx, dsns.migration)
	q := New(pool)
	_ = seedFixtures(t, ctx, q)

	if _, err := q.CreateRedactionRuleset(ctx, CreateRedactionRulesetParams{
		Version: 2, WorkspaceID: "ws_1", Rules: []byte(`{}`),
	}); err != nil {
		t.Fatalf("seed redaction_rulesets row: %v", err)
	}

	update := "UPDATE redaction_rulesets SET rules = '{\"tampered\":true}' WHERE version = 2"
	del := "DELETE FROM redaction_rulesets WHERE version = 2"

	// As the runtime role: this is the only connection any service ever
	// makes, and the grant alone should stop both statements.
	assertRejected(t, ctx, dsns.runtime, update, "runtime role UPDATE")
	assertRejected(t, ctx, dsns.runtime, del, "runtime role DELETE")

	// As the migration role (schema owner): grants don't bind the owner,
	// so only the trigger can stop this. Proves the trigger, not just the
	// grant, is doing the work.
	assertRejected(t, ctx, dsns.migration, update, "migration role UPDATE")
	assertRejected(t, ctx, dsns.migration, del, "migration role DELETE")
}

func TestQueries_WithTxAndClosedPoolErrors(t *testing.T) {
	ctx := context.Background()
	dsns := migratedPostgres(t, ctx)

	pool := mustConnect(t, ctx, dsns.migration)
	q := New(pool)
	s := seedFixtures(t, ctx, q)

	// WithTx is sqlc-generated scaffolding for running queries inside a
	// caller-supplied transaction; exercised here since future tasks
	// (sync-gateway's mutation intake) are the first real caller.
	if err := pgxTx(ctx, pool, func(tx pgx.Tx) error {
		txQueries := q.WithTx(tx)
		_, err := txQueries.GetCapture(ctx, s.captureID)
		return err
	}); err != nil {
		t.Fatalf("WithTx query: %v", err)
	}

	pool.Close()
	if _, err := q.ListCaptureEventsByCapture(ctx, s.captureID); err == nil {
		t.Fatal("expected query against closed pool to fail")
	}
}

func pgxTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// fakeRows is a minimal pgx.Rows double used to exercise the Scan-error and
// rows.Err() branches of the generated :many query, which a real Postgres
// round-trip has no easy way to provoke.
type fakeRows struct {
	pgx.Rows
	nexted  bool
	scanErr error
	rowsErr error
}

func (f *fakeRows) Next() bool {
	if f.nexted {
		return false
	}
	f.nexted = true
	return true
}

func (f *fakeRows) Scan(...any) error { return f.scanErr }
func (f *fakeRows) Err() error        { return f.rowsErr }
func (f *fakeRows) Close()            {}

type fakeDBTX struct {
	rows     pgx.Rows
	queryErr error
}

func (f *fakeDBTX) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (f *fakeDBTX) Query(context.Context, string, ...any) (pgx.Rows, error) {
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	return f.rows, nil
}
func (f *fakeDBTX) QueryRow(context.Context, string, ...any) pgx.Row { return nil }

func TestListCaptureEventsByCapture_QueryError(t *testing.T) {
	q := New(&fakeDBTX{queryErr: errQueryBoom})
	if _, err := q.ListCaptureEventsByCapture(context.Background(), "cap_x"); !errors.Is(err, errQueryBoom) {
		t.Fatalf("expected query error, got %v", err)
	}
}

func TestListCaptureEventsByCapture_ScanError(t *testing.T) {
	q := New(&fakeDBTX{rows: &fakeRows{scanErr: errScanBoom}})
	if _, err := q.ListCaptureEventsByCapture(context.Background(), "cap_x"); !errors.Is(err, errScanBoom) {
		t.Fatalf("expected scan error, got %v", err)
	}
}

func TestListCaptureEventsByCapture_RowsErr(t *testing.T) {
	q := New(&fakeDBTX{rows: &fakeRows{rowsErr: errRowsBoom}})
	if _, err := q.ListCaptureEventsByCapture(context.Background(), "cap_x"); !errors.Is(err, errRowsBoom) {
		t.Fatalf("expected rows.Err() error, got %v", err)
	}
}

func TestRuntimeRole_CannotWriteWorkspacesArbitrarily(t *testing.T) {
	// Sanity check that mutable tables (not append-only) DO allow the
	// runtime role to update, so the grant model in 00002_schema.sql isn't
	// accidentally locking down everything.
	ctx := context.Background()
	dsns := migratedPostgres(t, ctx)

	pool := mustConnect(t, ctx, dsns.migration)
	q := New(pool)
	s := seedFixtures(t, ctx, q)
	_ = s

	sqlDB, err := sql.Open("pgx", dsns.runtime)
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	if _, err := sqlDB.ExecContext(ctx, "UPDATE workspaces SET name = 'renamed' WHERE id = $1", s.workspaceID); err != nil {
		t.Fatalf("expected runtime role to update mutable table workspaces: %v", err)
	}
}
