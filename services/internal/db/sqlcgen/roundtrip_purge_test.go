// Round-trip tests for the query functions that exist only for
// capture-api/sync-gateway/redaction-audit/purge-job callers — never
// exercised by services/internal's own suite otherwise, but still gated at
// 100% coverage for this package.
package sqlcgen

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// TestManyQueries_ErrorBranches exercises the Query-error, Scan-error, and
// rows.Err() branches of every remaining generated :many query, mirroring
// TestListCaptureEventsByCapture_{QueryError,ScanError,RowsErr} above — a
// real Postgres round-trip has no easy way to provoke these, so a fake
// pgx.Rows/DBTX stands in.
func TestManyQueries_ErrorBranches(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name string
		call func(q *Queries) error
	}{
		{"ListAllAssetObjectKeys", func(q *Queries) error { _, err := q.ListAllAssetObjectKeys(ctx); return err }},
		{"ListAssetObjectKeysByCapture", func(q *Queries) error { _, err := q.ListAssetObjectKeysByCapture(ctx, "cap_x"); return err }},
		{"ListCapturesByWorkspace", func(q *Queries) error { _, err := q.ListCapturesByWorkspace(ctx, "ws_x"); return err }},
		{"ListCapturesPastRetention", func(q *Queries) error {
			_, err := q.ListCapturesPastRetention(ctx, ListCapturesPastRetentionParams{WorkspaceID: "ws_x"})
			return err
		}},
		{"ListMutationsSinceRevision", func(q *Queries) error {
			_, err := q.ListMutationsSinceRevision(ctx, ListMutationsSinceRevisionParams{WorkspaceID: "ws_x", Limit: 10})
			return err
		}},
		{"ListProjectsByWorkspace", func(q *Queries) error { _, err := q.ListProjectsByWorkspace(ctx, "ws_x"); return err }},
		{"ListPurgeJobObjectKeys", func(q *Queries) error { _, err := q.ListPurgeJobObjectKeys(ctx); return err }},
		{"ListPurgeJobsNotPurged", func(q *Queries) error { _, err := q.ListPurgeJobsNotPurged(ctx); return err }},
		{"ListReadyCapturesByWorkspace", func(q *Queries) error { _, err := q.ListReadyCapturesByWorkspace(ctx, "ws_x"); return err }},
		{"ListRedactionAuditFindingsByCapture", func(q *Queries) error {
			_, err := q.ListRedactionAuditFindingsByCapture(ctx, "cap_x")
			return err
		}},
		{"ListWorkspaces", func(q *Queries) error { _, err := q.ListWorkspaces(ctx); return err }},
		{"ListWorkspacesForUser", func(q *Queries) error { _, err := q.ListWorkspacesForUser(ctx, "user_x"); return err }},
	}

	for _, tc := range cases {
		t.Run(tc.name+"_QueryError", func(t *testing.T) {
			q := New(&fakeDBTX{queryErr: errQueryBoom})
			if err := tc.call(q); !errors.Is(err, errQueryBoom) {
				t.Fatalf("expected query error, got %v", err)
			}
		})
		t.Run(tc.name+"_ScanError", func(t *testing.T) {
			q := New(&fakeDBTX{rows: &fakeRows{scanErr: errScanBoom}})
			if err := tc.call(q); !errors.Is(err, errScanBoom) {
				t.Fatalf("expected scan error, got %v", err)
			}
		})
		t.Run(tc.name+"_RowsErr", func(t *testing.T) {
			q := New(&fakeDBTX{rows: &fakeRows{rowsErr: errRowsBoom}})
			if err := tc.call(q); !errors.Is(err, errRowsBoom) {
				t.Fatalf("expected rows.Err() error, got %v", err)
			}
		})
	}
}

// TestExecRowsQueries_ExecError exercises the Exec-error branch of the two
// generated :execrows queries.
func TestExecRowsQueries_ExecError(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name string
		call func(q *Queries) error
	}{
		{"DeleteAssetsForCapture", func(q *Queries) error { _, err := q.DeleteAssetsForCapture(ctx, "cap_x"); return err }},
		{"DeleteProjectForWorkspace", func(q *Queries) error {
			_, err := q.DeleteProjectForWorkspace(ctx, DeleteProjectForWorkspaceParams{ID: "proj_x", WorkspaceID: "ws_x"})
			return err
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name+"_ExecError", func(t *testing.T) {
			q := New(&fakeDBTX{execErr: errQueryBoom})
			if err := tc.call(q); !errors.Is(err, errQueryBoom) {
				t.Fatalf("expected exec error, got %v", err)
			}
		})
	}
}

func TestQueries_ListWorkspaces_RoundTrip(t *testing.T) {
	ctx := context.Background()
	dsns := migratedPostgres(t, ctx)

	pool := mustConnect(t, ctx, dsns.migration)
	q := New(pool)
	s := seedFixtures(t, ctx, q)

	got, err := q.ListWorkspaces(ctx)
	if err != nil || len(got) != 1 || got[0].ID != s.workspaceID {
		t.Fatalf("ListWorkspaces: %v, %+v", err, got)
	}
}

func TestQueries_ListWorkspacesForUser_RoundTrip(t *testing.T) {
	ctx := context.Background()
	dsns := migratedPostgres(t, ctx)

	pool := mustConnect(t, ctx, dsns.migration)
	q := New(pool)
	s := seedFixtures(t, ctx, q)

	if _, err := q.CreateMembership(ctx, CreateMembershipParams{
		ID: "mem_lwfu", WorkspaceID: s.workspaceID, UserID: s.userID, Role: "owner",
	}); err != nil {
		t.Fatalf("seed membership: %v", err)
	}

	got, err := q.ListWorkspacesForUser(ctx, s.userID)
	if err != nil || len(got) != 1 || got[0].ID != s.workspaceID {
		t.Fatalf("ListWorkspacesForUser: %v, %+v", err, got)
	}
	if got, err := q.ListWorkspacesForUser(ctx, "no_such_user"); err != nil || len(got) != 0 {
		t.Fatalf("ListWorkspacesForUser no membership: %v, %+v", err, got)
	}
}

func TestQueries_GetWorkspaceForMember_RoundTrip(t *testing.T) {
	ctx := context.Background()
	dsns := migratedPostgres(t, ctx)

	pool := mustConnect(t, ctx, dsns.migration)
	q := New(pool)
	s := seedFixtures(t, ctx, q)

	if _, err := q.CreateMembership(ctx, CreateMembershipParams{
		ID: "mem_gwfm", WorkspaceID: s.workspaceID, UserID: s.userID, Role: "owner",
	}); err != nil {
		t.Fatalf("seed membership: %v", err)
	}

	if got, err := q.GetWorkspaceForMember(ctx, GetWorkspaceForMemberParams{
		ID: s.workspaceID, UserID: s.userID,
	}); err != nil || got.ID != s.workspaceID {
		t.Fatalf("GetWorkspaceForMember: %v, %+v", err, got)
	}
	// Non-member looks identical to a nonexistent workspace.
	if _, err := q.GetWorkspaceForMember(ctx, GetWorkspaceForMemberParams{
		ID: s.workspaceID, UserID: "no_such_user",
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("GetWorkspaceForMember non-member: want ErrNoRows, got %v", err)
	}
}

func TestQueries_DeleteWorkspace_RoundTrip(t *testing.T) {
	ctx := context.Background()
	dsns := migratedPostgres(t, ctx)

	pool := mustConnect(t, ctx, dsns.migration)
	q := New(pool)

	ws, err := q.CreateWorkspace(ctx, CreateWorkspaceParams{ID: "ws_del", Name: "Deleteme"})
	if err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if err := q.DeleteWorkspace(ctx, ws.ID); err != nil {
		t.Fatalf("DeleteWorkspace: %v", err)
	}
	if _, err := q.GetWorkspace(ctx, ws.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("GetWorkspace after delete: want ErrNoRows, got %v", err)
	}
}

func TestQueries_GetProjectForWorkspace_And_ListProjectsByWorkspace_And_DeleteProjectForWorkspace_RoundTrip(t *testing.T) {
	ctx := context.Background()
	dsns := migratedPostgres(t, ctx)

	pool := mustConnect(t, ctx, dsns.migration)
	q := New(pool)
	s := seedFixtures(t, ctx, q)

	if got, err := q.GetProjectForWorkspace(ctx, GetProjectForWorkspaceParams{
		ID: s.projectID, WorkspaceID: s.workspaceID,
	}); err != nil || got.ID != s.projectID {
		t.Fatalf("GetProjectForWorkspace: %v, %+v", err, got)
	}
	if _, err := q.GetProjectForWorkspace(ctx, GetProjectForWorkspaceParams{
		ID: s.projectID, WorkspaceID: "some_other_workspace",
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("GetProjectForWorkspace wrong workspace: want ErrNoRows, got %v", err)
	}

	// A second, capture-free project so the delete below isn't blocked by
	// the FK from s.captureID's own project (captures_project_id_fkey).
	extra, err := q.CreateProject(ctx, CreateProjectParams{ID: "proj_del", WorkspaceID: s.workspaceID, Name: "Deleteme"})
	if err != nil {
		t.Fatalf("seed extra project: %v", err)
	}

	projects, err := q.ListProjectsByWorkspace(ctx, s.workspaceID)
	if err != nil || len(projects) != 2 {
		t.Fatalf("ListProjectsByWorkspace: %v, %+v", err, projects)
	}

	rows, err := q.DeleteProjectForWorkspace(ctx, DeleteProjectForWorkspaceParams{
		ID: extra.ID, WorkspaceID: "some_other_workspace",
	})
	if err != nil || rows != 0 {
		t.Fatalf("DeleteProjectForWorkspace wrong workspace: %v, rows=%d", err, rows)
	}
	rows, err = q.DeleteProjectForWorkspace(ctx, DeleteProjectForWorkspaceParams{
		ID: extra.ID, WorkspaceID: s.workspaceID,
	})
	if err != nil || rows != 1 {
		t.Fatalf("DeleteProjectForWorkspace: %v, rows=%d", err, rows)
	}
}

func TestQueries_GetMembershipByWorkspaceAndUser_RoundTrip(t *testing.T) {
	ctx := context.Background()
	dsns := migratedPostgres(t, ctx)

	pool := mustConnect(t, ctx, dsns.migration)
	q := New(pool)
	s := seedFixtures(t, ctx, q)

	membership, err := q.CreateMembership(ctx, CreateMembershipParams{
		ID: "mem_gbwau", WorkspaceID: s.workspaceID, UserID: s.userID, Role: "owner",
	})
	if err != nil {
		t.Fatalf("seed membership: %v", err)
	}

	if got, err := q.GetMembershipByWorkspaceAndUser(ctx, GetMembershipByWorkspaceAndUserParams{
		WorkspaceID: s.workspaceID, UserID: s.userID,
	}); err != nil || got.ID != membership.ID {
		t.Fatalf("GetMembershipByWorkspaceAndUser: %v, %+v", err, got)
	}
	if _, err := q.GetMembershipByWorkspaceAndUser(ctx, GetMembershipByWorkspaceAndUserParams{
		WorkspaceID: s.workspaceID, UserID: "no_such_user",
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("GetMembershipByWorkspaceAndUser no membership: want ErrNoRows, got %v", err)
	}
}

func TestQueries_GetLatestRedactionRuleset_RoundTrip(t *testing.T) {
	ctx := context.Background()
	dsns := migratedPostgres(t, ctx)

	pool := mustConnect(t, ctx, dsns.migration)
	q := New(pool)
	s := seedFixtures(t, ctx, q)

	if _, err := q.CreateRedactionRuleset(ctx, CreateRedactionRulesetParams{
		Version: s.rulesetVer + 1, WorkspaceID: s.workspaceID, Rules: []byte(`{}`),
	}); err != nil {
		t.Fatalf("seed second ruleset: %v", err)
	}

	got, err := q.GetLatestRedactionRuleset(ctx, s.workspaceID)
	if err != nil || got.Version != s.rulesetVer+1 {
		t.Fatalf("GetLatestRedactionRuleset: %v, %+v", err, got)
	}
}

func TestQueries_GetCaptureForWorkspace_And_ListCapturesByWorkspace_And_ListReadyCapturesByWorkspace_RoundTrip(t *testing.T) {
	ctx := context.Background()
	dsns := migratedPostgres(t, ctx)

	pool := mustConnect(t, ctx, dsns.migration)
	q := New(pool)
	s := seedFixtures(t, ctx, q)

	if got, err := q.GetCaptureForWorkspace(ctx, GetCaptureForWorkspaceParams{
		ID: s.captureID, WorkspaceID: s.workspaceID,
	}); err != nil || got.ID != s.captureID {
		t.Fatalf("GetCaptureForWorkspace: %v, %+v", err, got)
	}
	if _, err := q.GetCaptureForWorkspace(ctx, GetCaptureForWorkspaceParams{
		ID: s.captureID, WorkspaceID: "some_other_workspace",
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("GetCaptureForWorkspace wrong workspace: want ErrNoRows, got %v", err)
	}

	captures, err := q.ListCapturesByWorkspace(ctx, s.workspaceID)
	if err != nil || len(captures) != 1 || captures[0].ID != s.captureID {
		t.Fatalf("ListCapturesByWorkspace: %v, %+v", err, captures)
	}

	ready, err := q.ListReadyCapturesByWorkspace(ctx, s.workspaceID)
	if err != nil || len(ready) != 1 || ready[0].ID != s.captureID {
		t.Fatalf("ListReadyCapturesByWorkspace: %v, %+v", err, ready)
	}
}

func TestQueries_ListCapturesPastRetention_RoundTrip(t *testing.T) {
	ctx := context.Background()
	dsns := migratedPostgres(t, ctx)

	pool := mustConnect(t, ctx, dsns.migration)
	q := New(pool)
	s := seedFixtures(t, ctx, q)

	future := pgtype.Timestamptz{Time: time.Now().Add(24 * time.Hour), Valid: true}
	past, err := q.ListCapturesPastRetention(ctx, ListCapturesPastRetentionParams{
		WorkspaceID: s.workspaceID, Threshold: future,
	})
	if err != nil || len(past) != 1 || past[0].ID != s.captureID {
		t.Fatalf("ListCapturesPastRetention (future threshold): %v, %+v", err, past)
	}

	distant := pgtype.Timestamptz{Time: time.Now().Add(-24 * time.Hour), Valid: true}
	none, err := q.ListCapturesPastRetention(ctx, ListCapturesPastRetentionParams{
		WorkspaceID: s.workspaceID, Threshold: distant,
	})
	if err != nil || len(none) != 0 {
		t.Fatalf("ListCapturesPastRetention (past threshold): %v, %+v", err, none)
	}
}

func TestQueries_SetCaptureManifestComplete_RoundTrip(t *testing.T) {
	ctx := context.Background()
	dsns := migratedPostgres(t, ctx)

	pool := mustConnect(t, ctx, dsns.migration)
	q := New(pool)
	s := seedFixtures(t, ctx, q)

	got, err := q.SetCaptureManifestComplete(ctx, SetCaptureManifestCompleteParams{
		ID: s.captureID, ManifestComplete: false,
	})
	if err != nil || got.ManifestComplete {
		t.Fatalf("SetCaptureManifestComplete: %v, %+v", err, got)
	}
}

func TestQueries_ListMutationsSinceRevision_RoundTrip(t *testing.T) {
	ctx := context.Background()
	dsns := migratedPostgres(t, ctx)

	pool := mustConnect(t, ctx, dsns.migration)
	q := New(pool)
	s := seedFixtures(t, ctx, q)

	// CreateMutation leaves workspace_id at its '' default; InsertMutationIfNew
	// (used by sync-gateway) is the one that stamps it, which is what this
	// query filters on.
	if _, err := q.InsertMutationIfNew(ctx, InsertMutationIfNewParams{
		ID: "mut_lmsr", CaptureID: s.captureID, WorkspaceID: s.workspaceID, Op: "set_title", Payload: []byte(`{}`), ClientT: 1,
	}); err != nil {
		t.Fatalf("seed mutation: %v", err)
	}

	got, err := q.ListMutationsSinceRevision(ctx, ListMutationsSinceRevisionParams{
		WorkspaceID: s.workspaceID, Seq: pgtype.Int8{Int64: 0, Valid: true}, Limit: 10,
	})
	if err != nil || len(got) != 1 || got[0].ID != "mut_lmsr" {
		t.Fatalf("ListMutationsSinceRevision: %v, %+v", err, got)
	}
	// Seq at or above the only mutation's own seq excludes it (strictly >).
	if got, err := q.ListMutationsSinceRevision(ctx, ListMutationsSinceRevisionParams{
		WorkspaceID: s.workspaceID, Seq: pgtype.Int8{Int64: got0Seq(got), Valid: true}, Limit: 10,
	}); err != nil || len(got) != 0 {
		t.Fatalf("ListMutationsSinceRevision (past own seq): %v, %+v", err, got)
	}
}

func got0Seq(items []Mutation) int64 {
	if len(items) == 0 {
		return 0
	}
	return items[0].Seq.Int64
}

func TestQueries_GetAssetForWorkspace_RoundTrip(t *testing.T) {
	ctx := context.Background()
	dsns := migratedPostgres(t, ctx)

	pool := mustConnect(t, ctx, dsns.migration)
	q := New(pool)
	s := seedFixtures(t, ctx, q)

	asset, err := q.CreateAsset(ctx, CreateAssetParams{
		ID: "asset_gafw", CaptureID: s.captureID, Kind: "video", MimeType: "video/webm",
		SizeBytes: 1024, ChunkCount: 1, Sha256: pgtype.Text{String: "deadbeef", Valid: true},
		ObjectKey: "captures/cap_1/gafw",
	})
	if err != nil {
		t.Fatalf("seed asset: %v", err)
	}

	if got, err := q.GetAssetForWorkspace(ctx, GetAssetForWorkspaceParams{
		ID: asset.ID, CaptureID: s.captureID, WorkspaceID: s.workspaceID,
	}); err != nil || got.ID != asset.ID {
		t.Fatalf("GetAssetForWorkspace: %v, %+v", err, got)
	}
	if _, err := q.GetAssetForWorkspace(ctx, GetAssetForWorkspaceParams{
		ID: asset.ID, CaptureID: s.captureID, WorkspaceID: "some_other_workspace",
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("GetAssetForWorkspace wrong workspace: want ErrNoRows, got %v", err)
	}
}

func TestQueries_MarkAssetVerified_And_CountAssets_RoundTrip(t *testing.T) {
	ctx := context.Background()
	dsns := migratedPostgres(t, ctx)

	pool := mustConnect(t, ctx, dsns.migration)
	q := New(pool)
	s := seedFixtures(t, ctx, q)

	asset, err := q.CreateAsset(ctx, CreateAssetParams{
		ID: "asset_verify", CaptureID: s.captureID, Kind: "video", MimeType: "video/webm",
		SizeBytes: 1024, ChunkCount: 1, ObjectKey: "captures/cap_1/verify",
	})
	if err != nil {
		t.Fatalf("seed asset: %v", err)
	}

	if got, err := q.CountAssetsForCapture(ctx, s.captureID); err != nil || got != 1 {
		t.Fatalf("CountAssetsForCapture: %v, %d", err, got)
	}
	if got, err := q.CountUnverifiedAssetsForCapture(ctx, s.captureID); err != nil || got != 1 {
		t.Fatalf("CountUnverifiedAssetsForCapture (before verify): %v, %d", err, got)
	}

	verified, err := q.MarkAssetVerified(ctx, MarkAssetVerifiedParams{
		ID: asset.ID, Sha256: pgtype.Text{String: "cafebabe", Valid: true}, SizeBytes: 2048,
	})
	if err != nil || !verified.VerifiedAt.Valid || verified.SizeBytes != 2048 {
		t.Fatalf("MarkAssetVerified: %v, %+v", err, verified)
	}
	if got, err := q.CountUnverifiedAssetsForCapture(ctx, s.captureID); err != nil || got != 0 {
		t.Fatalf("CountUnverifiedAssetsForCapture (after verify): %v, %d", err, got)
	}
}

func TestQueries_AssetUploadPresign_RoundTrip(t *testing.T) {
	ctx := context.Background()
	dsns := migratedPostgres(t, ctx)

	pool := mustConnect(t, ctx, dsns.migration)
	q := New(pool)
	s := seedFixtures(t, ctx, q)

	asset, err := q.CreateAsset(ctx, CreateAssetParams{
		ID: "asset_presign", CaptureID: s.captureID, Kind: "video", MimeType: "video/webm",
		SizeBytes: 1024, ChunkCount: 1, ObjectKey: "captures/cap_1/presign",
	})
	if err != nil {
		t.Fatalf("seed asset: %v", err)
	}

	presign, err := q.CreateAssetUploadPresign(ctx, CreateAssetUploadPresignParams{
		ID: "presign_1", AssetID: asset.ID, ObjectKey: "uploads/presign_1",
		ChecksumSha256: "deadbeef", SizeBytes: 1024,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	if err != nil || presign.ConsumedAt.Valid {
		t.Fatalf("CreateAssetUploadPresign: %v, %+v", err, presign)
	}

	if got, err := q.GetAssetUploadPresignByKey(ctx, presign.ObjectKey); err != nil || got.ID != presign.ID {
		t.Fatalf("GetAssetUploadPresignByKey: %v, %+v", err, got)
	}

	consumed, err := q.ConsumeAssetUploadPresign(ctx, presign.ObjectKey)
	if err != nil || !consumed.ConsumedAt.Valid {
		t.Fatalf("ConsumeAssetUploadPresign: %v, %+v", err, consumed)
	}
	// Already-consumed rows are excluded by the WHERE clause: second consume
	// looks identical to no such presign.
	if _, err := q.ConsumeAssetUploadPresign(ctx, presign.ObjectKey); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("ConsumeAssetUploadPresign replay: want ErrNoRows, got %v", err)
	}
}

func TestQueries_ListAssetObjectKeys_RoundTrip(t *testing.T) {
	ctx := context.Background()
	dsns := migratedPostgres(t, ctx)

	pool := mustConnect(t, ctx, dsns.migration)
	q := New(pool)
	s := seedFixtures(t, ctx, q)

	if _, err := q.CreateAsset(ctx, CreateAssetParams{
		ID: "asset_keys", CaptureID: s.captureID, Kind: "video", MimeType: "video/webm",
		SizeBytes: 1024, ChunkCount: 1, ObjectKey: "captures/cap_1/keys",
	}); err != nil {
		t.Fatalf("seed asset: %v", err)
	}

	byCapture, err := q.ListAssetObjectKeysByCapture(ctx, s.captureID)
	if err != nil || len(byCapture) != 1 || byCapture[0] != "captures/cap_1/keys" {
		t.Fatalf("ListAssetObjectKeysByCapture: %v, %+v", err, byCapture)
	}

	all, err := q.ListAllAssetObjectKeys(ctx)
	if err != nil || len(all) != 1 || all[0] != "captures/cap_1/keys" {
		t.Fatalf("ListAllAssetObjectKeys: %v, %+v", err, all)
	}
}

func TestQueries_DeleteAssetsForCapture_RoundTrip(t *testing.T) {
	ctx := context.Background()
	dsns := migratedPostgres(t, ctx)

	pool := mustConnect(t, ctx, dsns.migration)
	q := New(pool)
	s := seedFixtures(t, ctx, q)

	if _, err := q.CreateAsset(ctx, CreateAssetParams{
		ID: "asset_del", CaptureID: s.captureID, Kind: "video", MimeType: "video/webm",
		SizeBytes: 1024, ChunkCount: 1, ObjectKey: "captures/cap_1/del",
	}); err != nil {
		t.Fatalf("seed asset: %v", err)
	}

	rows, err := q.DeleteAssetsForCapture(ctx, s.captureID)
	if err != nil || rows != 1 {
		t.Fatalf("DeleteAssetsForCapture: %v, rows=%d", err, rows)
	}
	if got, err := q.CountAssetsForCapture(ctx, s.captureID); err != nil || got != 0 {
		t.Fatalf("CountAssetsForCapture after delete: %v, %d", err, got)
	}
}

func TestQueries_RedactionAuditFindings_RoundTrip(t *testing.T) {
	ctx := context.Background()
	dsns := migratedPostgres(t, ctx)

	pool := mustConnect(t, ctx, dsns.migration)
	q := New(pool)
	s := seedFixtures(t, ctx, q)

	finding, err := q.CreateRedactionAuditFinding(ctx, CreateRedactionAuditFindingParams{
		ID: "finding_1", CaptureID: s.captureID, WorkspaceID: s.workspaceID,
		AppliedRulesetVersion:    pgtype.Int4{Int32: s.rulesetVer, Valid: true},
		EvaluationRulesetVersion: s.rulesetVer,
		RuleIds:                  []byte(`["rule_1"]`),
		EventIds:                 []byte(`["evt_1"]`),
	})
	if err != nil || finding.ID != "finding_1" {
		t.Fatalf("CreateRedactionAuditFinding: %v, %+v", err, finding)
	}

	got, err := q.ListRedactionAuditFindingsByCapture(ctx, s.captureID)
	if err != nil || len(got) != 1 || got[0].ID != finding.ID {
		t.Fatalf("ListRedactionAuditFindingsByCapture: %v, %+v", err, got)
	}
}

func TestQueries_PurgeJobLifecycle_RoundTrip(t *testing.T) {
	ctx := context.Background()
	dsns := migratedPostgres(t, ctx)

	pool := mustConnect(t, ctx, dsns.migration)
	q := New(pool)
	s := seedFixtures(t, ctx, q)

	job, err := q.CreatePurgeJobIfAbsent(ctx, CreatePurgeJobIfAbsentParams{
		ID: "purge_1", CaptureID: s.captureID, WorkspaceID: s.workspaceID,
	})
	if err != nil || job.State != "pending" {
		t.Fatalf("CreatePurgeJobIfAbsent: %v, %+v", err, job)
	}
	// ON CONFLICT DO NOTHING on capture_id: a second job for the same
	// capture is a no-op, surfaced as ErrNoRows.
	if _, err := q.CreatePurgeJobIfAbsent(ctx, CreatePurgeJobIfAbsentParams{
		ID: "purge_2", CaptureID: s.captureID, WorkspaceID: s.workspaceID,
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("CreatePurgeJobIfAbsent replay: want ErrNoRows, got %v", err)
	}

	if got, err := q.GetPurgeJob(ctx, job.ID); err != nil || got.ID != job.ID {
		t.Fatalf("GetPurgeJob: %v, %+v", err, got)
	}
	if got, err := q.GetPurgeJobByCapture(ctx, s.captureID); err != nil || got.ID != job.ID {
		t.Fatalf("GetPurgeJobByCapture: %v, %+v", err, got)
	}

	notPurged, err := q.ListPurgeJobsNotPurged(ctx)
	if err != nil || len(notPurged) != 1 || notPurged[0].ID != job.ID {
		t.Fatalf("ListPurgeJobsNotPurged: %v, %+v", err, notPurged)
	}

	tombstoned, err := q.TombstoneCaptureForPurge(ctx, s.captureID)
	if err != nil || tombstoned.State != "expired" {
		t.Fatalf("TombstoneCaptureForPurge: %v, %+v", err, tombstoned)
	}

	stateSet, err := q.SetPurgeJobState(ctx, SetPurgeJobStateParams{ID: job.ID, State: "tombstoned"})
	if err != nil || stateSet.State != "tombstoned" {
		t.Fatalf("SetPurgeJobState: %v, %+v", err, stateSet)
	}

	keysSet, err := q.SetPurgeJobObjectKeysAndState(ctx, SetPurgeJobObjectKeysAndStateParams{
		ID: job.ID, ObjectKeys: []byte(`["captures/cap_1/video"]`), State: "blobs-deleting",
	})
	if err != nil || keysSet.State != "blobs-deleting" {
		t.Fatalf("SetPurgeJobObjectKeysAndState: %v, %+v", err, keysSet)
	}

	allKeys, err := q.ListPurgeJobObjectKeys(ctx)
	if err != nil || len(allKeys) != 1 {
		t.Fatalf("ListPurgeJobObjectKeys: %v, %+v", err, allKeys)
	}

	cleared, err := q.ClearCaptureContentForPurge(ctx, s.captureID)
	if err != nil || cleared.ID != s.captureID {
		t.Fatalf("ClearCaptureContentForPurge: %v, %+v", err, cleared)
	}

	purged, err := q.SetPurgeJobState(ctx, SetPurgeJobStateParams{ID: job.ID, State: "purged"})
	if err != nil || purged.State != "purged" {
		t.Fatalf("SetPurgeJobState (purged): %v, %+v", err, purged)
	}
	// Terminal state excludes the job from the reconciliation sweep list.
	if notPurged, err := q.ListPurgeJobsNotPurged(ctx); err != nil || len(notPurged) != 0 {
		t.Fatalf("ListPurgeJobsNotPurged after purge: %v, %+v", err, notPurged)
	}
}
