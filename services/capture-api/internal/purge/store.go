// Package purge implements Task 13's durable hard-delete pipeline: a sweep
// that finds ready captures past their workspace's retention window, and a
// state machine that advances each one through pending -> tombstoned ->
// blobs-deleting -> blobs-deleted -> purged, one purge_jobs row per
// capture, safe to resume from any state after a crash. See Advance's doc
// comment for the state transitions and Reconcile/DetectOrphans for the
// sweep that re-drives stuck jobs and verifies no object was left behind.
package purge

import (
	"context"

	"github.com/dhiazfathra/how-to-replicate/services/internal/db/sqlcgen"
)

// Store is the narrow slice of sqlcgen.Querier the purge pipeline needs.
// The two multi-row transactional steps (tombstone, finalize) are not
// here — object storage cannot enlist in a Postgres transaction, so this
// package never needs a plain single-connection view for those; it calls
// through to capture-api/internal/store's pool-level
// TombstoneCaptureForPurge/FinalizePurgeForCapture instead, via the
// TxStore interface below.
type Store interface {
	ListWorkspaces(ctx context.Context) ([]sqlcgen.Workspace, error)
	ListCapturesPastRetention(ctx context.Context, arg sqlcgen.ListCapturesPastRetentionParams) ([]sqlcgen.Capture, error)
	CreatePurgeJobIfAbsent(ctx context.Context, arg sqlcgen.CreatePurgeJobIfAbsentParams) (sqlcgen.PurgeJob, error)
	GetPurgeJob(ctx context.Context, id string) (sqlcgen.PurgeJob, error)
	ListPurgeJobsNotPurged(ctx context.Context) ([]sqlcgen.PurgeJob, error)
	ListPurgeJobObjectKeys(ctx context.Context) ([][]byte, error)
	SetPurgeJobObjectKeysAndState(ctx context.Context, arg sqlcgen.SetPurgeJobObjectKeysAndStateParams) (sqlcgen.PurgeJob, error)
	ListAssetObjectKeysByCapture(ctx context.Context, captureID string) ([]string, error)
	ListAllAssetObjectKeys(ctx context.Context) ([]string, error)
}

// TxStore is the two purge steps that must each run as one Postgres
// transaction spanning captures + purge_jobs (+ audit_log) — see
// capture-api/internal/store.TombstoneCaptureForPurge and
// FinalizePurgeForCapture, which this interface exists only to let tests
// fake without a real database.
type TxStore interface {
	TombstoneCaptureForPurge(ctx context.Context, jobID, captureID, workspaceID string) (sqlcgen.PurgeJob, error)
	FinalizePurgeForCapture(ctx context.Context, jobID, captureID, workspaceID string) (sqlcgen.PurgeJob, error)
}

// BlobStore is the object-storage half of a purge: idempotent per-key
// delete (see services/internal/storage.Client.Delete's doc comment — a
// delete of an already-absent key is a success) and a full key listing for
// the orphan detector.
type BlobStore interface {
	Delete(ctx context.Context, key string) error
	ListKeys(ctx context.Context, prefix string) ([]string, error)
}
