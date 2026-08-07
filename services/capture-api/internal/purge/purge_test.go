package purge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/dhiazfathra/how-to-replicate/services/internal/db/sqlcgen"
)

// fakeStore is an in-memory double for Store+TxStore+BlobStore, exercising
// the same state transitions a real Postgres+MinIO pair would, but fast
// and deterministic — including the ability to simulate "the process died
// right after this state was written" by simply stopping and later
// resuming with a freshly-read job row, exactly like Reconcile does.
type fakeStore struct {
	workspaces []sqlcgen.Workspace
	captures   map[string]sqlcgen.Capture
	assets     map[string][]string // captureID -> object keys
	jobs       map[string]sqlcgen.PurgeJob
	byCapture  map[string]string // captureID -> jobID

	deletedKeys map[string]bool
	bucket      map[string]bool

	deleteErr                    error
	listWorkspacesErr            error
	listCapturesPastRetentionErr error
	listAssetKeysErr             error
	listAllAssetKeysErr          error
	listPurgeJobObjectKeysErr    error
	listKeysErr                  error
	tombstoneErr                 error
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		captures:    map[string]sqlcgen.Capture{},
		assets:      map[string][]string{},
		jobs:        map[string]sqlcgen.PurgeJob{},
		byCapture:   map[string]string{},
		deletedKeys: map[string]bool{},
		bucket:      map[string]bool{},
	}
}

func (f *fakeStore) ListWorkspaces(context.Context) ([]sqlcgen.Workspace, error) {
	if f.listWorkspacesErr != nil {
		return nil, f.listWorkspacesErr
	}
	return f.workspaces, nil
}

func (f *fakeStore) ListCapturesPastRetention(_ context.Context, arg sqlcgen.ListCapturesPastRetentionParams) ([]sqlcgen.Capture, error) {
	if f.listCapturesPastRetentionErr != nil {
		return nil, f.listCapturesPastRetentionErr
	}
	var out []sqlcgen.Capture
	for _, c := range f.captures {
		if c.WorkspaceID != arg.WorkspaceID || c.State != "ready" {
			continue
		}
		if c.CreatedAt.Time.After(arg.Threshold.Time) {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

func (f *fakeStore) CreatePurgeJobIfAbsent(_ context.Context, arg sqlcgen.CreatePurgeJobIfAbsentParams) (sqlcgen.PurgeJob, error) {
	if _, exists := f.byCapture[arg.CaptureID]; exists {
		return sqlcgen.PurgeJob{}, pgx.ErrNoRows
	}
	job := sqlcgen.PurgeJob{ID: arg.ID, CaptureID: arg.CaptureID, WorkspaceID: arg.WorkspaceID, State: StatePending, ObjectKeys: []byte("[]")}
	f.jobs[job.ID] = job
	f.byCapture[arg.CaptureID] = job.ID
	return job, nil
}

func (f *fakeStore) GetPurgeJob(_ context.Context, id string) (sqlcgen.PurgeJob, error) {
	job, ok := f.jobs[id]
	if !ok {
		return sqlcgen.PurgeJob{}, pgx.ErrNoRows
	}
	return job, nil
}

func (f *fakeStore) ListPurgeJobsNotPurged(context.Context) ([]sqlcgen.PurgeJob, error) {
	var out []sqlcgen.PurgeJob
	for _, j := range f.jobs {
		if j.State != StatePurged {
			out = append(out, j)
		}
	}
	return out, nil
}

func (f *fakeStore) ListPurgeJobObjectKeys(context.Context) ([][]byte, error) {
	if f.listPurgeJobObjectKeysErr != nil {
		return nil, f.listPurgeJobObjectKeysErr
	}
	var out [][]byte
	for _, j := range f.jobs {
		out = append(out, j.ObjectKeys)
	}
	return out, nil
}

func (f *fakeStore) SetPurgeJobObjectKeysAndState(_ context.Context, arg sqlcgen.SetPurgeJobObjectKeysAndStateParams) (sqlcgen.PurgeJob, error) {
	job, ok := f.jobs[arg.ID]
	if !ok {
		return sqlcgen.PurgeJob{}, pgx.ErrNoRows
	}
	job.ObjectKeys = arg.ObjectKeys
	job.State = arg.State
	f.jobs[arg.ID] = job
	return job, nil
}

func (f *fakeStore) ListAssetObjectKeysByCapture(_ context.Context, captureID string) ([]string, error) {
	if f.listAssetKeysErr != nil {
		return nil, f.listAssetKeysErr
	}
	return f.assets[captureID], nil
}

func (f *fakeStore) ListAllAssetObjectKeys(context.Context) ([]string, error) {
	if f.listAllAssetKeysErr != nil {
		return nil, f.listAllAssetKeysErr
	}
	var out []string
	for _, keys := range f.assets {
		out = append(out, keys...)
	}
	return out, nil
}

func (f *fakeStore) TombstoneCaptureForPurge(_ context.Context, jobID, captureID, _ string) (sqlcgen.PurgeJob, error) {
	if f.tombstoneErr != nil {
		return sqlcgen.PurgeJob{}, f.tombstoneErr
	}
	c, ok := f.captures[captureID]
	if !ok {
		return sqlcgen.PurgeJob{}, pgx.ErrNoRows
	}
	c.State = "expired"
	f.captures[captureID] = c

	job := f.jobs[jobID]
	job.State = StateTombstoned
	f.jobs[jobID] = job
	return job, nil
}

func (f *fakeStore) FinalizePurgeForCapture(_ context.Context, jobID, captureID, _ string) (sqlcgen.PurgeJob, error) {
	delete(f.assets, captureID)
	c := f.captures[captureID]
	c.Doc, c.Metadata, c.Env = []byte("{}"), []byte("{}"), []byte("{}")
	f.captures[captureID] = c

	job := f.jobs[jobID]
	job.State = StatePurged
	f.jobs[jobID] = job
	return job, nil
}

func (f *fakeStore) Delete(_ context.Context, key string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deletedKeys[key] = true
	delete(f.bucket, key)
	return nil
}

func (f *fakeStore) ListKeys(_ context.Context, _ string) ([]string, error) {
	if f.listKeysErr != nil {
		return nil, f.listKeysErr
	}
	var out []string
	for k := range f.bucket {
		out = append(out, k)
	}
	return out, nil
}

func newIDSeq() func() string {
	n := 0
	return func() string {
		n++
		return fmt.Sprintf("job_%d", n)
	}
}

func seedReadyCapture(f *fakeStore, id, workspaceID string, createdAt time.Time, keys ...string) {
	f.captures[id] = sqlcgen.Capture{
		ID:          id,
		WorkspaceID: workspaceID,
		State:       "ready",
		CreatedAt:   pgtype.Timestamptz{Time: createdAt, Valid: true},
		Doc:         []byte(`{"steps":["a"]}`),
		Metadata:    []byte(`{"x":1}`),
		Env:         []byte(`{"y":1}`),
	}
	f.assets[id] = append([]string{}, keys...)
	for _, k := range keys {
		f.bucket[k] = true
	}
}

func newPipeline(f *fakeStore) *Pipeline {
	return &Pipeline{Store: f, Tx: f, Blobs: f, NewID: newIDSeq()}
}

// --- Sweep ---

func TestSweep_CreatesJobForCaptureAndIsIdempotent(t *testing.T) {
	f := newFakeStore()
	f.workspaces = []sqlcgen.Workspace{{ID: "ws_1", PolicyOverrides: []byte(`{"retentionDays":30}`)}}
	seedReadyCapture(f, "cap_1", "ws_1", time.Now().Add(-40*24*time.Hour), "obj/cap_1/a")

	p := newPipeline(f)
	created, err := p.Sweep(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(created) != 1 || created[0].CaptureID != "cap_1" {
		t.Fatalf("created = %+v, want one job for cap_1", created)
	}

	// A repeat sweep must not create a second job for the same capture.
	created2, err := p.Sweep(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("Sweep (2nd): %v", err)
	}
	if len(created2) != 0 {
		t.Fatalf("2nd sweep created = %+v, want none (idempotent)", created2)
	}
}

func TestSweep_SkipsCaptureNotYetPastRetention(t *testing.T) {
	f := newFakeStore()
	f.workspaces = []sqlcgen.Workspace{{ID: "ws_1", PolicyOverrides: []byte(`{"retentionDays":90}`)}}
	seedReadyCapture(f, "cap_1", "ws_1", time.Now().Add(-1*24*time.Hour))

	p := newPipeline(f)
	created, err := p.Sweep(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(created) != 0 {
		t.Fatalf("created = %+v, want none (not past retention)", created)
	}
}

func TestSweep_SkipsWorkspaceWithNoRetentionChosen(t *testing.T) {
	f := newFakeStore()
	// No retentionDays override: policy.Defaults has no default, so
	// Resolve(...).Validate() fails and this workspace must be skipped
	// rather than swept against an implicit number nobody chose.
	f.workspaces = []sqlcgen.Workspace{{ID: "ws_1"}}
	seedReadyCapture(f, "cap_1", "ws_1", time.Now().Add(-1000*24*time.Hour))

	p := newPipeline(f)
	created, err := p.Sweep(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(created) != 0 {
		t.Fatalf("created = %+v, want none (no retention window chosen)", created)
	}
}

// --- Advance / RunToCompletion: kill-and-resume at every state ---

func TestRunToCompletion_FromPending_ReachesPurgedWithNoOrphans(t *testing.T) {
	f := newFakeStore()
	seedReadyCapture(f, "cap_1", "ws_1", time.Now(), "obj/cap_1/a", "obj/cap_1/b")
	job := sqlcgen.PurgeJob{ID: "job_1", CaptureID: "cap_1", WorkspaceID: "ws_1", State: StatePending, ObjectKeys: []byte("[]")}
	f.jobs[job.ID] = job

	p := newPipeline(f)
	final, err := p.RunToCompletion(context.Background(), job)
	if err != nil {
		t.Fatalf("RunToCompletion: %v", err)
	}
	if final.State != StatePurged {
		t.Fatalf("final state = %q, want purged", final.State)
	}
	if f.captures["cap_1"].State != "expired" {
		t.Fatalf("capture state = %q, want expired", f.captures["cap_1"].State)
	}
	if len(f.assets["cap_1"]) != 0 {
		t.Fatalf("asset rows survived purge: %v", f.assets["cap_1"])
	}
	if len(f.bucket) != 0 {
		t.Fatalf("blobs survived purge: %v", f.bucket)
	}

	orphans, err := p.DetectOrphans(context.Background(), "")
	if err != nil {
		t.Fatalf("DetectOrphans: %v", err)
	}
	if len(orphans) != 0 {
		t.Fatalf("orphans = %v, want none", orphans)
	}
}

// killAndResumeFrom simulates a process crash: it runs Advance exactly
// once from startState (landing the job in the next state, mirroring
// "the job died right after committing this transition"), re-reads the
// job row fresh (as a resumed process would after restart), and finishes
// via RunToCompletion. It asserts the end state is Purged with no
// surviving blobs and no orphaned objects — proof that a kill at
// startState resumes correctly.
func killAndResumeFrom(t *testing.T, startState string) {
	t.Helper()
	f := newFakeStore()
	seedReadyCapture(f, "cap_1", "ws_1", time.Now(), "obj/cap_1/a", "obj/cap_1/b")

	// A job reaching blobs-deleting/blobs-deleted for real would already
	// have its object_keys recorded from the tombstoned->blobs-deleting
	// step — seed that here so starting the simulation mid-pipeline
	// reflects a job that actually got there, not one missing state.
	objectKeys := []byte("[]")
	if startState == StateBlobsDeleting || startState == StateBlobsDeleted {
		objectKeys, _ = json.Marshal(f.assets["cap_1"])
	}
	job := sqlcgen.PurgeJob{ID: "job_1", CaptureID: "cap_1", WorkspaceID: "ws_1", State: startState, ObjectKeys: objectKeys}
	f.jobs[job.ID] = job
	if startState == StateBlobsDeleted {
		// blobs-deleted means the deletes already happened; only the
		// job-state write is what's left unresolved by the "crash".
		for _, k := range f.assets["cap_1"] {
			delete(f.bucket, k)
		}
	}

	p := newPipeline(f)

	// "the crash": drive one real step forward from startState, then
	// pretend the process died — read the row back fresh rather than
	// keeping the in-memory value RunToCompletion would otherwise reuse.
	afterOneStep, err := p.Advance(context.Background(), job)
	if err != nil {
		t.Fatalf("Advance from %s: %v", startState, err)
	}
	resumed, err := p.Store.GetPurgeJob(context.Background(), afterOneStep.ID)
	if err != nil {
		t.Fatalf("GetPurgeJob (simulating resume): %v", err)
	}

	final, err := p.RunToCompletion(context.Background(), resumed)
	if err != nil {
		t.Fatalf("RunToCompletion resumed from %s: %v", startState, err)
	}
	if final.State != StatePurged {
		t.Fatalf("resumed from %s: final state = %q, want purged", startState, final.State)
	}
	if len(f.bucket) != 0 {
		t.Fatalf("resumed from %s: blobs survived: %v", startState, f.bucket)
	}
	orphans, err := p.DetectOrphans(context.Background(), "")
	if err != nil {
		t.Fatalf("DetectOrphans: %v", err)
	}
	if len(orphans) != 0 {
		t.Fatalf("resumed from %s: orphans = %v, want none", startState, orphans)
	}
}

func TestKillAndResume_FromEveryState_ReachesPurged(t *testing.T) {
	for _, state := range []string{StatePending, StateTombstoned, StateBlobsDeleting, StateBlobsDeleted} {
		t.Run(state, func(t *testing.T) {
			killAndResumeFrom(t, state)
		})
	}
}

func TestAdvance_OnPurgedJob_IsNoOp(t *testing.T) {
	f := newFakeStore()
	job := sqlcgen.PurgeJob{ID: "job_1", CaptureID: "cap_1", WorkspaceID: "ws_1", State: StatePurged}
	f.jobs[job.ID] = job

	p := newPipeline(f)
	got, err := p.Advance(context.Background(), job)
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if got.State != StatePurged {
		t.Fatalf("state = %q, want purged unchanged", got.State)
	}
}

func TestAdvance_UnknownState_Errors(t *testing.T) {
	f := newFakeStore()
	p := newPipeline(f)
	_, err := p.Advance(context.Background(), sqlcgen.PurgeJob{ID: "job_1", State: "not-a-real-state"})
	if err == nil {
		t.Fatal("expected error for unknown state")
	}
}

// --- Idempotent blob delete ---

func TestBlobsDeleting_RepeatedDeleteSucceeds(t *testing.T) {
	f := newFakeStore()
	seedReadyCapture(f, "cap_1", "ws_1", time.Now(), "obj/cap_1/a")
	keys, _ := json.Marshal([]string{"obj/cap_1/a"})
	job := sqlcgen.PurgeJob{ID: "job_1", CaptureID: "cap_1", WorkspaceID: "ws_1", State: StateBlobsDeleting, ObjectKeys: keys}
	f.jobs[job.ID] = job

	p := newPipeline(f)

	// First delete removes the key from the fake bucket, same as a real
	// store's idempotent Delete would for a key that exists.
	got, err := p.Advance(context.Background(), job)
	if err != nil {
		t.Fatalf("Advance (1st delete): %v", err)
	}
	if got.State != StateBlobsDeleted {
		t.Fatalf("state = %q, want blobs-deleted", got.State)
	}

	// Re-running blobs-deleting against the same job (simulating a resume
	// that re-issues the delete for a key already gone) must still
	// succeed — services/internal/storage.Client.Delete's own idempotent
	// semantics are what this depends on; the fake models the same
	// contract by simply not erroring on an absent key.
	retry := sqlcgen.PurgeJob{ID: job.ID, CaptureID: job.CaptureID, WorkspaceID: job.WorkspaceID, State: StateBlobsDeleting, ObjectKeys: keys}
	got2, err := p.Advance(context.Background(), retry)
	if err != nil {
		t.Fatalf("Advance (repeat delete of already-absent key): %v", err)
	}
	if got2.State != StateBlobsDeleted {
		t.Fatalf("state = %q, want blobs-deleted", got2.State)
	}
}

func TestAdvance_BlobsDeleting_PropagatesDeleteError(t *testing.T) {
	f := newFakeStore()
	f.deleteErr = errors.New("object store unavailable")
	keys, _ := json.Marshal([]string{"obj/cap_1/a"})
	job := sqlcgen.PurgeJob{ID: "job_1", CaptureID: "cap_1", WorkspaceID: "ws_1", State: StateBlobsDeleting, ObjectKeys: keys}
	f.jobs[job.ID] = job

	p := newPipeline(f)
	if _, err := p.Advance(context.Background(), job); err == nil {
		t.Fatal("expected error to propagate from a failing blob delete")
	}
}

// --- Tombstone-before-reclaim ---

func TestTombstoned_UnreadableEvenWhileBlobsStillExist(t *testing.T) {
	f := newFakeStore()
	seedReadyCapture(f, "cap_1", "ws_1", time.Now(), "obj/cap_1/a")
	job := sqlcgen.PurgeJob{ID: "job_1", CaptureID: "cap_1", WorkspaceID: "ws_1", State: StatePending, ObjectKeys: []byte("[]")}
	f.jobs[job.ID] = job

	p := newPipeline(f)
	got, err := p.Advance(context.Background(), job)
	if err != nil {
		t.Fatalf("Advance (tombstone step): %v", err)
	}
	if got.State != StateTombstoned {
		t.Fatalf("job state = %q, want tombstoned", got.State)
	}

	// The compliance deadline (invariant 1's ready-gate) is met the
	// instant tombstoning commits — the capture is state=expired, which
	// every read path (gateCapture et al) already treats as non-ready —
	// even though its blob has not been deleted yet.
	if f.captures["cap_1"].State != "expired" {
		t.Fatalf("capture state = %q, want expired immediately on tombstone", f.captures["cap_1"].State)
	}
	if !f.bucket["obj/cap_1/a"] {
		t.Fatal("expected blob to still exist right after tombstoning (reclaim is a separate, later step)")
	}
}

// --- Reconciliation sweep ---

func TestReconcile_ReDrivesEveryStuckJobToPurged(t *testing.T) {
	f := newFakeStore()
	seedReadyCapture(f, "cap_pending", "ws_1", time.Now(), "obj/cap_pending/a")
	seedReadyCapture(f, "cap_deleting", "ws_1", time.Now(), "obj/cap_deleting/a")
	f.jobs["job_pending"] = sqlcgen.PurgeJob{ID: "job_pending", CaptureID: "cap_pending", WorkspaceID: "ws_1", State: StatePending, ObjectKeys: []byte("[]")}
	keys, _ := json.Marshal([]string{"obj/cap_deleting/a"})
	f.jobs["job_deleting"] = sqlcgen.PurgeJob{ID: "job_deleting", CaptureID: "cap_deleting", WorkspaceID: "ws_1", State: StateBlobsDeleting, ObjectKeys: keys}
	f.jobs["job_done"] = sqlcgen.PurgeJob{ID: "job_done", CaptureID: "cap_done", WorkspaceID: "ws_1", State: StatePurged}

	p := newPipeline(f)
	if err := p.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	for _, id := range []string{"job_pending", "job_deleting"} {
		if f.jobs[id].State != StatePurged {
			t.Fatalf("job %s state = %q, want purged after reconcile", id, f.jobs[id].State)
		}
	}
	if len(f.bucket) != 0 {
		t.Fatalf("blobs survived reconcile: %v", f.bucket)
	}
}

func TestReconcile_PropagatesAdvanceError(t *testing.T) {
	f := newFakeStore()
	f.jobs["job_bad"] = sqlcgen.PurgeJob{ID: "job_bad", CaptureID: "cap_missing", WorkspaceID: "ws_1", State: StatePending}
	p := newPipeline(f)
	if err := p.Reconcile(context.Background()); err == nil {
		t.Fatal("expected Reconcile to surface an Advance error (capture row missing)")
	}
}

// --- Orphan detector ---

func TestDetectOrphans_FindsObjectWithNoJobAndNoLiveAsset(t *testing.T) {
	f := newFakeStore()
	seedReadyCapture(f, "cap_1", "ws_1", time.Now(), "obj/cap_1/a")
	// A stray object nothing in Postgres accounts for: no asset row, no
	// purge_jobs row ever recorded it.
	f.bucket["obj/orphan/leftover"] = true

	p := newPipeline(f)
	orphans, err := p.DetectOrphans(context.Background(), "")
	if err != nil {
		t.Fatalf("DetectOrphans: %v", err)
	}
	if len(orphans) != 1 || orphans[0] != "obj/orphan/leftover" {
		t.Fatalf("orphans = %v, want exactly [obj/orphan/leftover]", orphans)
	}
}

// --- error propagation on every remaining Store/Blobs failure path ---

func TestSweep_PropagatesListWorkspacesError(t *testing.T) {
	f := newFakeStore()
	f.listWorkspacesErr = errors.New("boom")
	p := newPipeline(f)
	if _, err := p.Sweep(context.Background(), time.Now()); err == nil {
		t.Fatal("expected error")
	}
}

func TestSweep_PropagatesMalformedPolicyOverrides(t *testing.T) {
	f := newFakeStore()
	f.workspaces = []sqlcgen.Workspace{{ID: "ws_1", PolicyOverrides: []byte("not-json")}}
	p := newPipeline(f)
	if _, err := p.Sweep(context.Background(), time.Now()); err == nil {
		t.Fatal("expected error for malformed policy_overrides JSON")
	}
}

func TestSweep_PropagatesListCapturesPastRetentionError(t *testing.T) {
	f := newFakeStore()
	f.workspaces = []sqlcgen.Workspace{{ID: "ws_1", PolicyOverrides: []byte(`{"retentionDays":30}`)}}
	f.listCapturesPastRetentionErr = errors.New("boom")
	p := newPipeline(f)
	if _, err := p.Sweep(context.Background(), time.Now()); err == nil {
		t.Fatal("expected error")
	}
}

func TestAdvance_Pending_PropagatesTombstoneError(t *testing.T) {
	f := newFakeStore()
	f.tombstoneErr = errors.New("boom")
	job := sqlcgen.PurgeJob{ID: "job_1", CaptureID: "cap_1", WorkspaceID: "ws_1", State: StatePending}
	if _, err := (&Pipeline{Store: f, Tx: f, Blobs: f, NewID: newIDSeq()}).Advance(context.Background(), job); err == nil {
		t.Fatal("expected error")
	}
}

func TestAdvance_Tombstoned_PropagatesListAssetKeysError(t *testing.T) {
	f := newFakeStore()
	f.listAssetKeysErr = errors.New("boom")
	job := sqlcgen.PurgeJob{ID: "job_1", CaptureID: "cap_1", WorkspaceID: "ws_1", State: StateTombstoned}
	if _, err := newPipeline(f).Advance(context.Background(), job); err == nil {
		t.Fatal("expected error")
	}
}

func TestAdvance_BlobsDeleting_PropagatesMalformedObjectKeys(t *testing.T) {
	f := newFakeStore()
	job := sqlcgen.PurgeJob{ID: "job_1", CaptureID: "cap_1", WorkspaceID: "ws_1", State: StateBlobsDeleting, ObjectKeys: []byte("not-json")}
	if _, err := newPipeline(f).Advance(context.Background(), job); err == nil {
		t.Fatal("expected error for malformed object_keys JSON")
	}
}

func TestReconcile_PropagatesListPurgeJobsError(t *testing.T) {
	// ListPurgeJobsNotPurged has no dedicated error knob on fakeStore, but
	// Reconcile's own error-wrapping branch is already exercised via
	// TestReconcile_PropagatesAdvanceError; this covers the
	// ListPurgeJobsNotPurged failure path with a store that fails outright.
	f := &erroringPurgeJobsStore{fakeStore: newFakeStore(), err: errors.New("boom")}
	p := &Pipeline{Store: f, Tx: f.fakeStore, Blobs: f.fakeStore, NewID: newIDSeq()}
	if err := p.Reconcile(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

// erroringPurgeJobsStore overrides only ListPurgeJobsNotPurged so
// TestReconcile_PropagatesListPurgeJobsError can hit Reconcile's own
// error-wrapping branch without adding a rarely-used knob to fakeStore.
type erroringPurgeJobsStore struct {
	*fakeStore
	err error
}

func (e *erroringPurgeJobsStore) ListPurgeJobsNotPurged(context.Context) ([]sqlcgen.PurgeJob, error) {
	return nil, e.err
}

func TestDetectOrphans_PropagatesListKeysError(t *testing.T) {
	f := newFakeStore()
	f.listKeysErr = errors.New("boom")
	if _, err := newPipeline(f).DetectOrphans(context.Background(), ""); err == nil {
		t.Fatal("expected error")
	}
}

func TestDetectOrphans_PropagatesListAllAssetKeysError(t *testing.T) {
	f := newFakeStore()
	f.listAllAssetKeysErr = errors.New("boom")
	if _, err := newPipeline(f).DetectOrphans(context.Background(), ""); err == nil {
		t.Fatal("expected error")
	}
}

func TestDetectOrphans_PropagatesListPurgeJobObjectKeysError(t *testing.T) {
	f := newFakeStore()
	f.listPurgeJobObjectKeysErr = errors.New("boom")
	if _, err := newPipeline(f).DetectOrphans(context.Background(), ""); err == nil {
		t.Fatal("expected error")
	}
}

func TestDetectOrphans_PropagatesMalformedJobObjectKeys(t *testing.T) {
	f := newFakeStore()
	f.jobs["job_1"] = sqlcgen.PurgeJob{ID: "job_1", ObjectKeys: []byte("not-json")}
	if _, err := newPipeline(f).DetectOrphans(context.Background(), ""); err == nil {
		t.Fatal("expected error for malformed purge_jobs.object_keys")
	}
}

func TestDetectOrphans_KeyStillInFlightJobIsNotAnOrphan(t *testing.T) {
	f := newFakeStore()
	// No live asset row (already removed), but a purge_jobs row (any
	// state, including purged — retained briefly as evidence) still
	// records the key: it must not be reported as an orphan.
	f.bucket["obj/cap_1/a"] = true
	keys, _ := json.Marshal([]string{"obj/cap_1/a"})
	f.jobs["job_1"] = sqlcgen.PurgeJob{ID: "job_1", CaptureID: "cap_1", WorkspaceID: "ws_1", State: StatePurged, ObjectKeys: keys}
	// A pending job with no object_keys recorded yet (empty/nil) must not
	// blow up the unmarshal loop — it's simply skipped.
	f.jobs["job_2"] = sqlcgen.PurgeJob{ID: "job_2", CaptureID: "cap_2", WorkspaceID: "ws_1", State: StatePending}

	p := newPipeline(f)
	orphans, err := p.DetectOrphans(context.Background(), "")
	if err != nil {
		t.Fatalf("DetectOrphans: %v", err)
	}
	if len(orphans) != 0 {
		t.Fatalf("orphans = %v, want none (key is accounted for by a job row)", orphans)
	}
}
