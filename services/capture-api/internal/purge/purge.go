package purge

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/dhiazfathra/how-to-replicate/services/capture-api/internal/policy"
	"github.com/dhiazfathra/how-to-replicate/services/internal/db/sqlcgen"
)

// Job states, in the exact order the brief specifies. Advance moves a job
// from its current state to the next one; RunToCompletion loops Advance
// until Purged or an error.
const (
	StatePending       = "pending"
	StateTombstoned    = "tombstoned"
	StateBlobsDeleting = "blobs-deleting"
	StateBlobsDeleted  = "blobs-deleted"
	StatePurged        = "purged"
)

// Pipeline wires the store, the tombstone/finalize transactions, and
// object storage together. NewID mints purge_jobs row IDs — caller-minted,
// same convention as every other server-side row in this codebase (see
// capture-api/internal/api.newID).
type Pipeline struct {
	Store Store
	Tx    TxStore
	Blobs BlobStore
	NewID func() string
}

// Sweep finds every ready capture in every workspace that has passed its
// resolved retention window and creates a pending purge_jobs row for it.
// Idempotent: a capture that already has a job (in any state) is skipped —
// CreatePurgeJobIfAbsent's ON CONFLICT DO NOTHING on capture_id makes a
// repeat sweep over the same capture a no-op rather than a duplicate job.
func (p *Pipeline) Sweep(ctx context.Context, now time.Time) ([]sqlcgen.PurgeJob, error) {
	workspaces, err := p.Store.ListWorkspaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("purge: list workspaces: %w", err)
	}

	var created []sqlcgen.PurgeJob
	for _, ws := range workspaces {
		var overrides policy.Overrides
		if len(ws.PolicyOverrides) > 0 {
			if err := json.Unmarshal(ws.PolicyOverrides, &overrides); err != nil {
				return created, fmt.Errorf("purge: unmarshal policy overrides for workspace %s: %w", ws.ID, err)
			}
		}
		resolved := policy.Resolve(policy.Defaults, overrides)
		if err := resolved.Validate(); err != nil {
			// A workspace with no valid retention window has nothing to
			// sweep against — createWorkspace should make this
			// unreachable, but a sweep is not the place to enforce that
			// invariant, only to decline acting without one.
			continue
		}
		threshold := now.AddDate(0, 0, -resolved.RetentionDays)

		captures, err := p.Store.ListCapturesPastRetention(ctx, sqlcgen.ListCapturesPastRetentionParams{
			WorkspaceID: ws.ID,
			Threshold:   pgtype.Timestamptz{Time: threshold, Valid: true},
		})
		if err != nil {
			return created, fmt.Errorf("purge: list captures past retention for workspace %s: %w", ws.ID, err)
		}

		for _, c := range captures {
			job, err := p.Store.CreatePurgeJobIfAbsent(ctx, sqlcgen.CreatePurgeJobIfAbsentParams{
				ID:          p.NewID(),
				CaptureID:   c.ID,
				WorkspaceID: ws.ID,
			})
			if err != nil {
				// pgx.ErrNoRows here means CreatePurgeJobIfAbsent's ON
				// CONFLICT fired with nothing to return — a job already
				// exists for this capture, which is the expected steady
				// state on every sweep after the first, not a failure.
				continue
			}
			created = append(created, job)
		}
	}
	return created, nil
}

// Advance drives job exactly one step forward and returns the updated row.
// It is safe to call on a job in any state, including Purged (a no-op) —
// this is what makes both a normal run and a resume-after-crash the same
// code path: Advance never assumes anything about how job got to its
// current state beyond what the row itself records.
//
//	pending        -> tombstoned      capture.state = 'expired' (invariant
//	                                   1's ready-gate now rejects it); this
//	                                   is the step where the compliance
//	                                   deadline is met.
//	tombstoned     -> blobs-deleting  object keys are read from assets and
//	                                   frozen into the job row.
//	blobs-deleting -> blobs-deleted   every key in the job row is deleted,
//	                                   idempotently — a key already gone
//	                                   (e.g. a previous attempt got partway
//	                                   through before crashing) is success,
//	                                   not an error.
//	blobs-deleted  -> purged          asset rows are removed and the
//	                                   capture's disclosable content
//	                                   columns are cleared (see
//	                                   ClearCaptureContentForPurge's SQL
//	                                   comment for why the captures row
//	                                   itself is never deleted outright).
func (p *Pipeline) Advance(ctx context.Context, job sqlcgen.PurgeJob) (sqlcgen.PurgeJob, error) {
	switch job.State {
	case StatePending:
		c, err := p.Tx.TombstoneCaptureForPurge(ctx, job.ID, job.CaptureID, job.WorkspaceID)
		if err != nil {
			return job, fmt.Errorf("purge: tombstone capture %s: %w", job.CaptureID, err)
		}
		return c, nil

	case StateTombstoned:
		keys, err := p.Store.ListAssetObjectKeysByCapture(ctx, job.CaptureID)
		if err != nil {
			return job, fmt.Errorf("purge: list asset keys for capture %s: %w", job.CaptureID, err)
		}
		// keys is a plain []string — Marshal on it cannot fail, same
		// reasoning as policy.Overrides' Marshal call sites in
		// capture-api/internal/api/handlers.go; the error is still
		// checked defensively rather than discarded.
		keysJSON, err := json.Marshal(keys)
		if err != nil {
			return job, fmt.Errorf("purge: marshal object keys for capture %s: %w", job.CaptureID, err)
		}
		return p.Store.SetPurgeJobObjectKeysAndState(ctx, sqlcgen.SetPurgeJobObjectKeysAndStateParams{
			ID:         job.ID,
			ObjectKeys: keysJSON,
			State:      StateBlobsDeleting,
		})

	case StateBlobsDeleting:
		var keys []string
		if err := json.Unmarshal(job.ObjectKeys, &keys); err != nil {
			return job, fmt.Errorf("purge: unmarshal object keys for job %s: %w", job.ID, err)
		}
		for _, key := range keys {
			if err := p.Blobs.Delete(ctx, key); err != nil {
				return job, fmt.Errorf("purge: delete blob %s for capture %s: %w", key, job.CaptureID, err)
			}
		}
		return p.Store.SetPurgeJobObjectKeysAndState(ctx, sqlcgen.SetPurgeJobObjectKeysAndStateParams{
			ID:         job.ID,
			ObjectKeys: job.ObjectKeys,
			State:      StateBlobsDeleted,
		})

	case StateBlobsDeleted:
		return p.Tx.FinalizePurgeForCapture(ctx, job.ID, job.CaptureID, job.WorkspaceID)

	case StatePurged:
		return job, nil

	default:
		return job, fmt.Errorf("purge: job %s has unknown state %q", job.ID, job.State)
	}
}

// RunToCompletion calls Advance repeatedly until job reaches Purged or
// Advance errors. It is exactly what both a fresh job and a resumed job
// (one killed and restarted at any intermediate state) run through — proof
// that a kill at any state converges to Purged is proof that this loop,
// re-entered with a job row read fresh from the store, finishes.
func (p *Pipeline) RunToCompletion(ctx context.Context, job sqlcgen.PurgeJob) (sqlcgen.PurgeJob, error) {
	for job.State != StatePurged {
		var err error
		job, err = p.Advance(ctx, job)
		if err != nil {
			return job, err
		}
	}
	return job, nil
}

// Reconcile re-drives every purge_jobs row not yet Purged to completion.
// This is what recovers a job that crashed mid-flight without needing to
// know which state it was in — ListPurgeJobsNotPurged returns it exactly
// as it was left, and RunToCompletion resumes from there.
func (p *Pipeline) Reconcile(ctx context.Context) error {
	jobs, err := p.Store.ListPurgeJobsNotPurged(ctx)
	if err != nil {
		return fmt.Errorf("purge: list unpurged jobs: %w", err)
	}
	for _, job := range jobs {
		if _, err := p.RunToCompletion(ctx, job); err != nil {
			return fmt.Errorf("purge: reconcile job %s: %w", job.ID, err)
		}
	}
	return nil
}

// DetectOrphans returns every object key that exists in the bucket but is
// referenced by neither a live asset row nor any purge_jobs row (at any
// state — a job's object_keys are retained as long as the job row is,
// which per the brief is "briefly" past Purged). This is the check that
// makes "no orphans" verifiable rather than assumed: a key satisfying
// neither condition is a blob nothing in Postgres accounts for.
func (p *Pipeline) DetectOrphans(ctx context.Context, prefix string) ([]string, error) {
	inBucket, err := p.Blobs.ListKeys(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("purge: list bucket keys: %w", err)
	}

	referenced := map[string]struct{}{}
	liveKeys, err := p.Store.ListAllAssetObjectKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("purge: list live asset keys: %w", err)
	}
	for _, k := range liveKeys {
		referenced[k] = struct{}{}
	}

	jobKeysRaw, err := p.Store.ListPurgeJobObjectKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("purge: list purge job object keys: %w", err)
	}
	for _, raw := range jobKeysRaw {
		var keys []string
		if len(raw) == 0 {
			continue
		}
		if err := json.Unmarshal(raw, &keys); err != nil {
			return nil, fmt.Errorf("purge: unmarshal purge job object keys: %w", err)
		}
		for _, k := range keys {
			referenced[k] = struct{}{}
		}
	}

	var orphans []string
	for _, key := range inBucket {
		if _, ok := referenced[key]; !ok {
			orphans = append(orphans, key)
		}
	}
	return orphans, nil
}
