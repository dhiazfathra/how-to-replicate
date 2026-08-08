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

// TestPgStore_PullDeltas_Integration proves Task 6's delta log against real
// Postgres: mutations.seq/workspace_id are populated correctly by
// InsertMutationIfNew, and PullMutationsSince's WHERE/ORDER BY/LIMIT round
// trip through the real driver (pgtype.Int8 in particular — see
// ListMutationsSinceRevisionParams.Seq), not just the in-memory fake.
func TestPgStore_PullDeltas_Integration(t *testing.T) {
	ctx := context.Background()
	pool, workspaceID := setupRuntimePool(t, ctx)
	gw := &Gateway{WithinTx: NewTxRunner(pool), Now: func() time.Time { return time.Unix(1700000000, 0) }}

	batch := []*syncv1.Mutation{
		{Id: "pd1", CaptureId: "cap_int", Op: &syncv1.Mutation_SetTitle{SetTitle: &syncv1.SetTitle{Title: "first"}}},
		{Id: "pd2", CaptureId: "cap_int", Op: &syncv1.Mutation_SetSummary{SetSummary: &syncv1.SetSummary{Summary: "second"}}},
		{Id: "pd3", CaptureId: "cap_int", Op: &syncv1.Mutation_AppendComment{AppendComment: &syncv1.AppendComment{CommentId: "pdc1", Body: "third"}}},
	}
	if _, err := gw.PushMutations(ctx, workspaceID, "user_int", batch); err != nil {
		t.Fatalf("PushMutations: %v", err)
	}

	all, revision, hasMore, captures, err := gw.PullDeltas(ctx, workspaceID, 0, 0)
	if err != nil {
		t.Fatalf("PullDeltas: %v", err)
	}
	if hasMore {
		t.Fatalf("want hasMore=false for 3 rows under the default limit")
	}
	if len(all) != 3 || all[0].GetId() != "pd1" || all[1].GetId() != "pd2" || all[2].GetId() != "pd3" {
		t.Fatalf("want [pd1 pd2 pd3] in order, got %+v", all)
	}
	if all[2].GetAppendComment().GetCommentId() != "pdc1" || all[2].GetAppendComment().GetBody() != "third" {
		t.Fatalf("append_comment did not round-trip through Postgres: %+v", all[2])
	}
	// One CaptureSyncState for cap_int, sourced from the real captures row
	// — manifest_complete false (no assets uploaded in this test) and
	// revision matching the cursor PullDeltas itself returned.
	if len(captures) != 1 || captures[0].GetCaptureId() != "cap_int" {
		t.Fatalf("want one CaptureSyncState for cap_int, got %+v", captures)
	}
	if captures[0].GetManifestComplete() {
		t.Fatalf("want manifest_complete=false against real Postgres, got true")
	}
	if captures[0].GetRevision() != revision {
		t.Fatalf("want CaptureSyncState revision %d to match PullDeltas cursor, got %d", revision, captures[0].GetRevision())
	}

	// since= the last-seen revision must exclude everything already seen —
	// the core "delta not snapshot" behavior, now against a real cursor.
	empty, _, _, _, err := gw.PullDeltas(ctx, workspaceID, revision, 0)
	if err != nil {
		t.Fatalf("PullDeltas at head: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("want no deltas pulling from the current head, got %+v", empty)
	}

	// A page size smaller than the total sets has_more and stops exactly at
	// the boundary.
	page, pageRevision, pageHasMore, _, err := gw.PullDeltas(ctx, workspaceID, 0, 2)
	if err != nil {
		t.Fatalf("PullDeltas paged: %v", err)
	}
	if !pageHasMore || len(page) != 2 || page[1].GetId() != "pd2" {
		t.Fatalf("want a 2-row page with hasMore=true, got page=%+v hasMore=%v", page, pageHasMore)
	}
	rest, _, restHasMore, _, err := gw.PullDeltas(ctx, workspaceID, pageRevision, 2)
	if err != nil {
		t.Fatalf("PullDeltas rest of page: %v", err)
	}
	if restHasMore || len(rest) != 1 || rest[0].GetId() != "pd3" {
		t.Fatalf("want the final row with hasMore=false, got rest=%+v hasMore=%v", rest, restHasMore)
	}

	// A different workspace must see none of these deltas.
	otherWorkspace, err := sqlcgen.New(pool).CreateWorkspace(ctx, sqlcgen.CreateWorkspaceParams{ID: "ws_int_other", Name: "Other"})
	if err != nil {
		t.Fatalf("seed other workspace: %v", err)
	}
	isolated, _, _, _, err := gw.PullDeltas(ctx, otherWorkspace.ID, 0, 0)
	if err != nil {
		t.Fatalf("PullDeltas other workspace: %v", err)
	}
	if len(isolated) != 0 {
		t.Fatalf("want no cross-workspace leakage, got %+v", isolated)
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

	t.Run("PullMutationsSince on a cancelled context", func(t *testing.T) {
		if _, err := store.PullMutationsSince(cancelled, workspaceID, 0, 10); err == nil {
			t.Fatalf("want error, got nil")
		}
	})

	t.Run("InsertMutationIfNew against a nonexistent capture", func(t *testing.T) {
		if _, err := store.InsertMutationIfNew(ctx, "mx1", "does_not_exist", workspaceID, "set_title", []byte(`"x"`), 0); err == nil {
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

	t.Run("GetAssetForWorkspace on a cancelled context", func(t *testing.T) {
		if _, _, err := store.GetAssetForWorkspace(cancelled, "no_asset", "cap_int", workspaceID); err == nil {
			t.Fatalf("want error, got nil")
		}
	})

	t.Run("GetAssetForWorkspace not found", func(t *testing.T) {
		_, found, err := store.GetAssetForWorkspace(ctx, "no_such_asset", "cap_int", workspaceID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if found {
			t.Fatalf("want found=false for a nonexistent asset")
		}
	})

	t.Run("UpsertAssetForUpload against a nonexistent capture", func(t *testing.T) {
		if _, err := store.UpsertAssetForUpload(ctx, Asset{ID: "ax1", CaptureID: "does_not_exist", Kind: "video", MimeType: "video/mp4", SizeBytes: 1, ObjectKey: "k1"}); err == nil {
			t.Fatalf("want FK-violation error, got nil")
		}
	})

	t.Run("CreateAssetUploadPresign against a nonexistent asset", func(t *testing.T) {
		if err := store.CreateAssetUploadPresign(ctx, AssetUploadPresign{ID: "k-missing", AssetID: "does_not_exist", ObjectKey: "k-missing", ChecksumSHA256: "x", SizeBytes: 1, ExpiresAt: time.Now()}); err == nil {
			t.Fatalf("want FK-violation error, got nil")
		}
	})

	t.Run("GetAssetUploadPresignByKey on a cancelled context", func(t *testing.T) {
		if _, _, err := store.GetAssetUploadPresignByKey(cancelled, "k1"); err == nil {
			t.Fatalf("want error, got nil")
		}
	})

	t.Run("GetAssetUploadPresignByKey not found", func(t *testing.T) {
		_, found, err := store.GetAssetUploadPresignByKey(ctx, "no-such-key")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if found {
			t.Fatalf("want found=false for a nonexistent presign")
		}
	})

	t.Run("ConsumeAssetUploadPresign on a cancelled context", func(t *testing.T) {
		if _, err := store.ConsumeAssetUploadPresign(cancelled, "k1"); err == nil {
			t.Fatalf("want error, got nil")
		}
	})

	t.Run("ConsumeAssetUploadPresign not found", func(t *testing.T) {
		ok, err := store.ConsumeAssetUploadPresign(ctx, "no-such-key")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ok {
			t.Fatalf("want ok=false for a nonexistent presign")
		}
	})

	t.Run("MarkAssetVerified against a nonexistent asset", func(t *testing.T) {
		if err := store.MarkAssetVerified(ctx, "does_not_exist", "abc", 1); err == nil {
			t.Fatalf("want no-rows error, got nil")
		}
	})

	t.Run("ManifestComplete on a cancelled context", func(t *testing.T) {
		if _, err := store.ManifestComplete(cancelled, "cap_int"); err == nil {
			t.Fatalf("want error, got nil")
		}
	})

	t.Run("SetCaptureManifestComplete against a nonexistent capture", func(t *testing.T) {
		if err := store.SetCaptureManifestComplete(ctx, "does_not_exist", true); err == nil {
			t.Fatalf("want no-rows error, got nil")
		}
	})
}

// TestPgStore_Assets_Integration exercises the full asset-manifest write
// path directly against pgStore: create, upsert-on-re-request, presign
// lifecycle, and manifest completion counting, against real Postgres rather
// than the in-memory fake.
func TestPgStore_Assets_Integration(t *testing.T) {
	ctx := context.Background()
	pool, workspaceID := setupRuntimePool(t, ctx)
	store := &pgStore{q: sqlcgen.New(pool)}

	asset, err := store.UpsertAssetForUpload(ctx, Asset{
		ID: "asset_pg1", CaptureID: "cap_int", Kind: "video", MimeType: "video/mp4",
		SizeBytes: 10, ObjectKey: "key-1",
	})
	if err != nil {
		t.Fatalf("upsert asset: %v", err)
	}
	if asset.ObjectKey != "key-1" || asset.Verified {
		t.Fatalf("unexpected initial asset state: %+v", asset)
	}

	fetched, found, err := store.GetAssetForWorkspace(ctx, "asset_pg1", "cap_int", workspaceID)
	if err != nil || !found {
		t.Fatalf("get asset: found=%v err=%v", found, err)
	}
	if fetched.ObjectKey != "key-1" {
		t.Fatalf("unexpected fetched asset: %+v", fetched)
	}

	// Re-request repoints object_key without touching verification state.
	reRequested, err := store.UpsertAssetForUpload(ctx, Asset{
		ID: "asset_pg1", CaptureID: "cap_int", Kind: "video", MimeType: "video/mp4",
		SizeBytes: 10, ObjectKey: "key-2",
	})
	if err != nil {
		t.Fatalf("re-upsert asset: %v", err)
	}
	if reRequested.ObjectKey != "key-2" {
		t.Fatalf("want repointed object key, got %+v", reRequested)
	}

	complete, err := store.ManifestComplete(ctx, "cap_int")
	if err != nil {
		t.Fatalf("manifest complete: %v", err)
	}
	if complete {
		t.Fatalf("want incomplete manifest before verification")
	}

	if err := store.CreateAssetUploadPresign(ctx, AssetUploadPresign{
		ID: "key-2", AssetID: "asset_pg1", ObjectKey: "key-2",
		ChecksumSHA256: "abc", SizeBytes: 10, ExpiresAt: time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatalf("create presign: %v", err)
	}

	presign, found, err := store.GetAssetUploadPresignByKey(ctx, "key-2")
	if err != nil || !found {
		t.Fatalf("get presign: found=%v err=%v", found, err)
	}
	if presign.ChecksumSHA256 != "abc" {
		t.Fatalf("unexpected presign: %+v", presign)
	}

	ok, err := store.ConsumeAssetUploadPresign(ctx, "key-2")
	if err != nil || !ok {
		t.Fatalf("consume presign: ok=%v err=%v", ok, err)
	}
	// A second consume must report ok=false without erroring.
	ok2, err := store.ConsumeAssetUploadPresign(ctx, "key-2")
	if err != nil || ok2 {
		t.Fatalf("want ok=false on replay consume, got ok=%v err=%v", ok2, err)
	}

	if err := store.MarkAssetVerified(ctx, "asset_pg1", "deadbeef", 10); err != nil {
		t.Fatalf("mark verified: %v", err)
	}

	complete, err = store.ManifestComplete(ctx, "cap_int")
	if err != nil {
		t.Fatalf("manifest complete: %v", err)
	}
	if !complete {
		t.Fatalf("want complete manifest after the only asset verifies")
	}

	if err := store.SetCaptureManifestComplete(ctx, "cap_int", true); err != nil {
		t.Fatalf("set manifest complete: %v", err)
	}

	q := sqlcgen.New(pool)
	capture, err := q.GetCapture(ctx, "cap_int")
	if err != nil {
		t.Fatalf("get capture: %v", err)
	}
	if !capture.ManifestComplete {
		t.Fatalf("want manifest_complete persisted true")
	}
}

// TestPgStore_GetCaptureManifestState_Integration proves pgStore's
// GetCaptureManifestState against real Postgres: the found path returns the
// real manifest_complete/revision columns, and an unknown capture ID
// reports found=false rather than an error (same no-existence-leak shape
// as LockCaptureForWorkspace).
func TestPgStore_GetCaptureManifestState_Integration(t *testing.T) {
	ctx := context.Background()
	pool, _ := setupRuntimePool(t, ctx)
	store := &pgStore{q: sqlcgen.New(pool)}

	if err := store.SetCaptureManifestComplete(ctx, "cap_int", true); err != nil {
		t.Fatalf("set manifest complete: %v", err)
	}

	complete, _, found, err := store.GetCaptureManifestState(ctx, "cap_int")
	if err != nil {
		t.Fatalf("GetCaptureManifestState: %v", err)
	}
	if !found || !complete {
		t.Fatalf("want found=true complete=true, got found=%v complete=%v", found, complete)
	}

	_, _, found, err = store.GetCaptureManifestState(ctx, "cap_does_not_exist")
	if err != nil {
		t.Fatalf("GetCaptureManifestState for unknown capture: %v", err)
	}
	if found {
		t.Fatalf("want found=false for an unknown capture id")
	}
}
