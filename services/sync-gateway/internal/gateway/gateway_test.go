package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	syncv1 "github.com/dhiazfathra/how-to-replicate/proto/gen/go/sync/v1"
)

// fakeStore is an in-memory Store used by every test in this file — no
// database required, per the brief's "unit-testable without a database"
// requirement.
type fakeStore struct {
	captures      map[string]Capture // captureID -> capture
	mutationSeen  map[string]bool
	fieldVersions map[string]FieldVersion // captureID+"/"+field -> version
	comments      []insertedComment
	superseded    []supersededRecord

	// error injection for infra-failure test cases
	lockErr       error
	insertMutErr  error
	fieldGetErr   error
	fieldSetErr   error
	updateErr     error
	commentErr    error
	supersededErr error

	// asset-upload state (Task 5) — see assets_test.go.
	assets              map[string]Asset              // assetID -> asset
	presigns            map[string]AssetUploadPresign // objectKey -> presign
	consumedPresigns    map[string]bool               // objectKey -> consumed
	manifestComplete    map[string]bool               // captureID -> flag
	getAssetErr         error
	upsertAssetErr      error
	createPresignErr    error
	getPresignErr       error
	consumePresignErr   error
	markVerifiedErr     error
	manifestCompleteErr error
	setManifestErr      error
}

type insertedComment struct {
	id, captureID, authorID, body string
}

type supersededRecord struct {
	workspaceID, captureID, mutationID, field string
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		captures:         map[string]Capture{},
		mutationSeen:     map[string]bool{},
		fieldVersions:    map[string]FieldVersion{},
		assets:           map[string]Asset{},
		presigns:         map[string]AssetUploadPresign{},
		consumedPresigns: map[string]bool{},
		manifestComplete: map[string]bool{},
	}
}

func (f *fakeStore) key(captureID, field string) string { return captureID + "/" + field }

func (f *fakeStore) LockCaptureForWorkspace(_ context.Context, captureID, workspaceID string) (Capture, bool, error) {
	if f.lockErr != nil {
		return Capture{}, false, f.lockErr
	}
	c, ok := f.captures[captureID]
	if !ok || c.WorkspaceID != workspaceID {
		return Capture{}, false, nil
	}
	return c, true, nil
}

func (f *fakeStore) InsertMutationIfNew(_ context.Context, mutationID, _, _ string, _ []byte, _ int64) (bool, error) {
	if f.insertMutErr != nil {
		return false, f.insertMutErr
	}
	if f.mutationSeen[mutationID] {
		return false, nil
	}
	f.mutationSeen[mutationID] = true
	return true, nil
}

func (f *fakeStore) FieldVersion(_ context.Context, captureID, field string) (FieldVersion, bool, error) {
	if f.fieldGetErr != nil {
		return FieldVersion{}, false, f.fieldGetErr
	}
	v, ok := f.fieldVersions[f.key(captureID, field)]
	return v, ok, nil
}

func (f *fakeStore) SetFieldVersion(_ context.Context, captureID, field string, v FieldVersion) error {
	if f.fieldSetErr != nil {
		return f.fieldSetErr
	}
	f.fieldVersions[f.key(captureID, field)] = v
	return nil
}

func (f *fakeStore) UpdateCaptureRevisionAndDoc(_ context.Context, captureID string, revision int64, doc []byte) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	c := f.captures[captureID]
	c.Revision = revision
	c.Doc = doc
	f.captures[captureID] = c
	return nil
}

func (f *fakeStore) InsertComment(_ context.Context, commentID, captureID, authorID, body string) error {
	if f.commentErr != nil {
		return f.commentErr
	}
	f.comments = append(f.comments, insertedComment{commentID, captureID, authorID, body})
	return nil
}

func (f *fakeStore) RecordSupersededMutation(_ context.Context, workspaceID, captureID, mutationID, field string) error {
	if f.supersededErr != nil {
		return f.supersededErr
	}
	f.superseded = append(f.superseded, supersededRecord{workspaceID, captureID, mutationID, field})
	return nil
}

// withinTx runs fn directly against store — no real transaction needed for
// an in-memory fake.
func withinTx(store *fakeStore) func(context.Context, func(context.Context, Store) error) error {
	return func(ctx context.Context, fn func(context.Context, Store) error) error {
		return fn(ctx, store)
	}
}

func newGateway(store *fakeStore, now time.Time) *Gateway {
	return &Gateway{
		WithinTx: withinTx(store),
		Now:      func() time.Time { return now },
	}
}

const (
	workspaceA = "ws_a"
	workspaceB = "ws_b"
	captureID  = "cap_1"
)

func seedCapture(store *fakeStore, workspaceID string) {
	store.captures[captureID] = Capture{ID: captureID, WorkspaceID: workspaceID, Revision: 0, Doc: nil}
}

func docField(t *testing.T, doc []byte, field string) any {
	t.Helper()
	m := map[string]any{}
	if len(doc) > 0 {
		if err := json.Unmarshal(doc, &m); err != nil {
			t.Fatalf("unmarshal doc: %v", err)
		}
	}
	return m[field]
}

func setTitleMutation(id, title string) *syncv1.Mutation {
	return &syncv1.Mutation{
		Id:        id,
		CaptureId: captureID,
		Op:        &syncv1.Mutation_SetTitle{SetTitle: &syncv1.SetTitle{Title: title}},
	}
}

// TestPushMutations_TableDriven covers each operation kind applying
// cleanly against a fresh capture with no prior field state.
func TestPushMutations_TableDriven(t *testing.T) {
	tests := []struct {
		name      string
		mutation  *syncv1.Mutation
		wantField string
		wantValue any
	}{
		{
			name:      "set_title",
			mutation:  &syncv1.Mutation{Id: "m1", CaptureId: captureID, Op: &syncv1.Mutation_SetTitle{SetTitle: &syncv1.SetTitle{Title: "Checkout fails"}}},
			wantField: fieldTitle,
			wantValue: "Checkout fails",
		},
		{
			name:      "set_summary",
			mutation:  &syncv1.Mutation{Id: "m2", CaptureId: captureID, Op: &syncv1.Mutation_SetSummary{SetSummary: &syncv1.SetSummary{Summary: "Repro on Safari"}}},
			wantField: fieldSummary,
			wantValue: "Repro on Safari",
		},
		{
			name:      "set_tags",
			mutation:  &syncv1.Mutation{Id: "m3", CaptureId: captureID, Op: &syncv1.Mutation_SetTags{SetTags: &syncv1.SetTags{Tags: []string{"bug", "safari"}}}},
			wantField: fieldTags,
			wantValue: []any{"bug", "safari"},
		},
		{
			name:      "assign",
			mutation:  &syncv1.Mutation{Id: "m4", CaptureId: captureID, Op: &syncv1.Mutation_Assign{Assign: &syncv1.Assign{AssigneeUserId: "user_1"}}},
			wantField: fieldAssignment,
			wantValue: "user_1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore()
			seedCapture(store, workspaceA)
			gw := newGateway(store, time.Unix(100, 0))

			results, err := gw.PushMutations(context.Background(), workspaceA, "user_x", []*syncv1.Mutation{tt.mutation})
			if err != nil {
				t.Fatalf("PushMutations: %v", err)
			}
			if len(results) != 1 || !results[0].Applied || results[0].Reason != ReasonNone {
				t.Fatalf("want applied result, got %+v", results)
			}

			capture := store.captures[captureID]
			if capture.Revision != 1 {
				t.Fatalf("want revision 1, got %d", capture.Revision)
			}
			got := docField(t, capture.Doc, tt.wantField)
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tt.wantValue)
			if string(gotJSON) != string(wantJSON) {
				t.Fatalf("field %q: got %s, want %s", tt.wantField, gotJSON, wantJSON)
			}
		})
	}
}

// TestPushMutations_AppendCommentNeverConflicts proves two comment
// mutations both apply — appends never conflict, unlike set/assign.
func TestPushMutations_AppendCommentNeverConflicts(t *testing.T) {
	store := newFakeStore()
	seedCapture(store, workspaceA)
	gw := newGateway(store, time.Unix(100, 0))

	m1 := &syncv1.Mutation{Id: "c1", CaptureId: captureID, Op: &syncv1.Mutation_AppendComment{AppendComment: &syncv1.AppendComment{CommentId: "cm1", Body: "first"}}}
	m2 := &syncv1.Mutation{Id: "c2", CaptureId: captureID, Op: &syncv1.Mutation_AppendComment{AppendComment: &syncv1.AppendComment{CommentId: "cm2", Body: "second"}}}

	results, err := gw.PushMutations(context.Background(), workspaceA, "user_x", []*syncv1.Mutation{m1, m2})
	if err != nil {
		t.Fatalf("PushMutations: %v", err)
	}
	for _, r := range results {
		if !r.Applied {
			t.Fatalf("want both comments applied, got %+v", r)
		}
	}
	if len(store.comments) != 2 {
		t.Fatalf("want 2 comments recorded, got %d", len(store.comments))
	}
	if store.captures[captureID].Revision != 2 {
		t.Fatalf("want revision advanced twice, got %d", store.captures[captureID].Revision)
	}
}

// TestPushMutations_IdempotentReplay proves a full batch replay after
// resending is safe: same results, no double revision advance, no
// duplicate comment insert.
func TestPushMutations_IdempotentReplay(t *testing.T) {
	store := newFakeStore()
	seedCapture(store, workspaceA)
	gw := newGateway(store, time.Unix(100, 0))

	batch := []*syncv1.Mutation{
		setTitleMutation("m1", "Checkout fails"),
		{Id: "c1", CaptureId: captureID, Op: &syncv1.Mutation_AppendComment{AppendComment: &syncv1.AppendComment{CommentId: "cm1", Body: "note"}}},
	}

	first, err := gw.PushMutations(context.Background(), workspaceA, "user_x", batch)
	if err != nil {
		t.Fatalf("first push: %v", err)
	}
	firstRevision := store.captures[captureID].Revision

	second, err := gw.PushMutations(context.Background(), workspaceA, "user_x", batch)
	if err != nil {
		t.Fatalf("replay push: %v", err)
	}

	if len(second) != len(first) {
		t.Fatalf("replay result count mismatch: %d vs %d", len(second), len(first))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("replay result %d changed: %+v vs %+v", i, first[i], second[i])
		}
	}
	if store.captures[captureID].Revision != firstRevision {
		t.Fatalf("replay advanced revision: %d -> %d", firstRevision, store.captures[captureID].Revision)
	}
	if len(store.comments) != 1 {
		t.Fatalf("replay duplicated the comment insert: %d comments", len(store.comments))
	}
}

// TestPushMutations_ConflictResolution exercises every tie-break level of
// the (server-received timestamp, revision, mutation ID) tuple.
func TestPushMutations_ConflictResolution(t *testing.T) {
	t0 := time.Unix(1000, 0)
	t1 := t0.Add(time.Second)

	tests := []struct {
		name          string
		existing      FieldVersion
		candidateT    time.Time
		candidateRev  int64
		candidateID   string
		candidateWins bool
	}{
		{
			name:          "later timestamp wins outright",
			existing:      FieldVersion{ServerT: t0, Revision: 5, MutationID: "z"},
			candidateT:    t1,
			candidateRev:  1, // lower revision, still wins: timestamp is primary
			candidateID:   "a",
			candidateWins: true,
		},
		{
			name:          "earlier timestamp loses outright",
			existing:      FieldVersion{ServerT: t1, Revision: 1, MutationID: "a"},
			candidateT:    t0,
			candidateRev:  99, // higher revision, still loses: timestamp is primary
			candidateID:   "z",
			candidateWins: false,
		},
		{
			name:          "equal timestamp: higher revision breaks the tie",
			existing:      FieldVersion{ServerT: t0, Revision: 3, MutationID: "m"},
			candidateT:    t0,
			candidateRev:  4,
			candidateID:   "a", // lexicographically smaller, would lose on ID alone
			candidateWins: true,
		},
		{
			name:          "equal timestamp: lower revision loses the tie",
			existing:      FieldVersion{ServerT: t0, Revision: 5, MutationID: "a"},
			candidateT:    t0,
			candidateRev:  4,
			candidateID:   "z", // lexicographically larger, would win on ID alone
			candidateWins: false,
		},
		{
			name:          "equal timestamp and revision: larger mutation ID wins",
			existing:      FieldVersion{ServerT: t0, Revision: 3, MutationID: "aaa"},
			candidateT:    t0,
			candidateRev:  3,
			candidateID:   "bbb",
			candidateWins: true,
		},
		{
			name:          "equal timestamp and revision: smaller mutation ID loses",
			existing:      FieldVersion{ServerT: t0, Revision: 3, MutationID: "bbb"},
			candidateT:    t0,
			candidateRev:  3,
			candidateID:   "aaa",
			candidateWins: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wins(FieldVersion{ServerT: tt.candidateT, Revision: tt.candidateRev, MutationID: tt.candidateID}, tt.existing)
			if got != tt.candidateWins {
				t.Fatalf("wins() = %v, want %v", got, tt.candidateWins)
			}
		})
	}
}

// TestPushMutations_ConflictResolutionEndToEnd drives the tie-break
// through PushMutations itself (not just the wins() helper), proving a
// losing mutation still advances revision and gets audited, while the
// doc keeps the winner's value.
func TestPushMutations_ConflictResolutionEndToEnd(t *testing.T) {
	store := newFakeStore()
	seedCapture(store, workspaceA)

	t0 := time.Unix(1000, 0)
	gwEarly := newGateway(store, t0)
	if _, err := gwEarly.PushMutations(context.Background(), workspaceA, "user_x", []*syncv1.Mutation{setTitleMutation("m1", "First title")}); err != nil {
		t.Fatalf("seed mutation: %v", err)
	}

	// A mutation with an earlier server-received timestamp arrives after
	// (e.g. redelivered), naming the same field. It must lose, but is
	// still "applied" (accepted, recorded, revision advances) and superseded.
	tEarlier := t0.Add(-time.Minute)
	gwLate := newGateway(store, tEarlier)
	results, err := gwLate.PushMutations(context.Background(), workspaceA, "user_x", []*syncv1.Mutation{setTitleMutation("m0", "Stale title")})
	if err != nil {
		t.Fatalf("late mutation: %v", err)
	}
	if !results[0].Applied || results[0].Reason != ReasonNone {
		t.Fatalf("losing mutation should still be Applied (accepted, just superseded): %+v", results[0])
	}

	capture := store.captures[captureID]
	if got := docField(t, capture.Doc, fieldTitle); got != "First title" {
		t.Fatalf("doc should keep the winner's title, got %v", got)
	}
	if capture.Revision != 2 {
		t.Fatalf("both accepted mutations should advance revision: got %d", capture.Revision)
	}
	if len(store.superseded) != 1 || store.superseded[0].mutationID != "m0" {
		t.Fatalf("want m0 recorded as superseded, got %+v", store.superseded)
	}
}

// TestPushMutations_DifferentFieldsBothApply proves LWW is per field:
// two mutations on the same capture touching different fields both win.
func TestPushMutations_DifferentFieldsBothApply(t *testing.T) {
	store := newFakeStore()
	seedCapture(store, workspaceA)
	gw := newGateway(store, time.Unix(100, 0))

	batch := []*syncv1.Mutation{
		setTitleMutation("m1", "New title"),
		{Id: "m2", CaptureId: captureID, Op: &syncv1.Mutation_SetSummary{SetSummary: &syncv1.SetSummary{Summary: "New summary"}}},
	}
	results, err := gw.PushMutations(context.Background(), workspaceA, "user_x", batch)
	if err != nil {
		t.Fatalf("PushMutations: %v", err)
	}
	for _, r := range results {
		if !r.Applied {
			t.Fatalf("want both field writes applied, got %+v", r)
		}
	}
	capture := store.captures[captureID]
	if got := docField(t, capture.Doc, fieldTitle); got != "New title" {
		t.Fatalf("title not applied: %v", got)
	}
	if got := docField(t, capture.Doc, fieldSummary); got != "New summary" {
		t.Fatalf("summary not applied: %v", got)
	}
}

// TestPushMutations_CrossWorkspaceRejectionIsNonDisclosing proves a
// mutation naming a capture in a different workspace is rejected with the
// exact same reason as a mutation naming a capture that doesn't exist —
// the caller cannot tell the two apart.
func TestPushMutations_CrossWorkspaceRejectionIsNonDisclosing(t *testing.T) {
	store := newFakeStore()
	seedCapture(store, workspaceB) // capture exists, but in workspace B
	gw := newGateway(store, time.Unix(100, 0))

	crossWorkspace, err := gw.PushMutations(context.Background(), workspaceA, "user_x", []*syncv1.Mutation{setTitleMutation("m1", "x")})
	if err != nil {
		t.Fatalf("PushMutations: %v", err)
	}

	unknown, err := gw.PushMutations(context.Background(), workspaceA, "user_x", []*syncv1.Mutation{
		{Id: "m2", CaptureId: "does_not_exist", Op: &syncv1.Mutation_SetTitle{SetTitle: &syncv1.SetTitle{Title: "x"}}},
	})
	if err != nil {
		t.Fatalf("PushMutations: %v", err)
	}

	if crossWorkspace[0].Applied || crossWorkspace[0].Reason != ReasonNotFound {
		t.Fatalf("cross-workspace mutation should be rejected as not_found, got %+v", crossWorkspace[0])
	}
	if unknown[0].Applied || unknown[0].Reason != ReasonNotFound {
		t.Fatalf("unknown-capture mutation should be rejected as not_found, got %+v", unknown[0])
	}
	if crossWorkspace[0].Reason != unknown[0].Reason {
		t.Fatalf("cross-workspace and unknown-capture must produce identical reasons: %q vs %q", crossWorkspace[0].Reason, unknown[0].Reason)
	}
	// No mutation row and no revision bump — the caller never touched the
	// foreign capture in workspace B.
	if store.mutationSeen["m1"] {
		t.Fatalf("rejected mutation must not be recorded")
	}
	if store.captures[captureID].Revision != 0 {
		t.Fatalf("rejected mutation must not advance the foreign capture's revision")
	}
}

// TestPushMutations_InvalidMutation covers structurally invalid input:
// missing ID, missing capture ID, and no op set.
func TestPushMutations_InvalidMutation(t *testing.T) {
	tests := []struct {
		name     string
		mutation *syncv1.Mutation
	}{
		{"missing id", &syncv1.Mutation{CaptureId: captureID, Op: &syncv1.Mutation_SetTitle{SetTitle: &syncv1.SetTitle{Title: "x"}}}},
		{"missing capture id", &syncv1.Mutation{Id: "m1", Op: &syncv1.Mutation_SetTitle{SetTitle: &syncv1.SetTitle{Title: "x"}}}},
		{"no op set", &syncv1.Mutation{Id: "m1", CaptureId: captureID}},
		{"append comment missing comment id", &syncv1.Mutation{Id: "m1", CaptureId: captureID, Op: &syncv1.Mutation_AppendComment{AppendComment: &syncv1.AppendComment{Body: "x"}}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore()
			seedCapture(store, workspaceA)
			gw := newGateway(store, time.Unix(100, 0))

			results, err := gw.PushMutations(context.Background(), workspaceA, "user_x", []*syncv1.Mutation{tt.mutation})
			if err != nil {
				t.Fatalf("PushMutations: %v", err)
			}
			if results[0].Applied || results[0].Reason != ReasonInvalidMutation {
				t.Fatalf("want invalid_mutation rejection, got %+v", results[0])
			}
		})
	}
}

// TestPushMutations_StoreErrorsPropagate proves an infra failure at any
// Store call surfaces as an error from PushMutations, not as a silent or
// mislabeled per-mutation result.
func TestPushMutations_StoreErrorsPropagate(t *testing.T) {
	boom := errors.New("boom")

	tests := []struct {
		name    string
		corrupt func(*fakeStore)
	}{
		{"lock error", func(s *fakeStore) { s.lockErr = boom }},
		{"insert mutation error", func(s *fakeStore) { s.insertMutErr = boom }},
		{"field version get error", func(s *fakeStore) { s.fieldGetErr = boom }},
		{"field version set error", func(s *fakeStore) { s.fieldSetErr = boom }},
		{"update capture error", func(s *fakeStore) { s.updateErr = boom }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore()
			seedCapture(store, workspaceA)
			tt.corrupt(store)
			gw := newGateway(store, time.Unix(100, 0))

			_, err := gw.PushMutations(context.Background(), workspaceA, "user_x", []*syncv1.Mutation{setTitleMutation("m1", "x")})
			if err == nil {
				t.Fatalf("want error, got nil")
			}
		})
	}
}

// TestPushMutations_CorruptDocPropagatesOnWinningWrite covers applyOne's
// setDocField error branch: a winning field write against a capture whose
// stored doc is already corrupt must surface as an error.
func TestPushMutations_CorruptDocPropagatesOnWinningWrite(t *testing.T) {
	store := newFakeStore()
	seedCapture(store, workspaceA)
	store.captures[captureID] = Capture{ID: captureID, WorkspaceID: workspaceA, Revision: 0, Doc: []byte("not json")}
	gw := newGateway(store, time.Unix(100, 0))

	_, err := gw.PushMutations(context.Background(), workspaceA, "user_x", []*syncv1.Mutation{setTitleMutation("m1", "x")})
	if err == nil {
		t.Fatalf("want error, got nil")
	}
}

// TestPushMutations_CommentStoreErrorsPropagate covers the append-comment
// path's own error sites, not exercised by the set/assign cases above.
func TestPushMutations_CommentStoreErrorsPropagate(t *testing.T) {
	boom := errors.New("boom")

	tests := []struct {
		name    string
		corrupt func(*fakeStore)
	}{
		{"insert comment error", func(s *fakeStore) { s.commentErr = boom }},
		{"update capture error", func(s *fakeStore) { s.updateErr = boom }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore()
			seedCapture(store, workspaceA)
			tt.corrupt(store)
			gw := newGateway(store, time.Unix(100, 0))

			m := &syncv1.Mutation{Id: "c1", CaptureId: captureID, Op: &syncv1.Mutation_AppendComment{AppendComment: &syncv1.AppendComment{CommentId: "cm1", Body: "x"}}}
			_, err := gw.PushMutations(context.Background(), workspaceA, "user_x", []*syncv1.Mutation{m})
			if err == nil {
				t.Fatalf("want error, got nil")
			}
		})
	}
}

// TestPushMutations_SupersededAuditErrorPropagates covers the one error
// site only reachable via the losing branch of conflict resolution.
func TestPushMutations_SupersededAuditErrorPropagates(t *testing.T) {
	store := newFakeStore()
	seedCapture(store, workspaceA)
	t0 := time.Unix(1000, 0)

	gwEarly := newGateway(store, t0)
	if _, err := gwEarly.PushMutations(context.Background(), workspaceA, "user_x", []*syncv1.Mutation{setTitleMutation("m1", "First")}); err != nil {
		t.Fatalf("seed mutation: %v", err)
	}

	store.supersededErr = errors.New("boom")
	gwLate := newGateway(store, t0.Add(-time.Minute))
	_, err := gwLate.PushMutations(context.Background(), workspaceA, "user_x", []*syncv1.Mutation{setTitleMutation("m0", "Stale")})
	if err == nil {
		t.Fatalf("want error, got nil")
	}
}

// TestPushMutations_EmptyBatch proves an empty batch is a no-op, not an
// error.
func TestPushMutations_EmptyBatch(t *testing.T) {
	store := newFakeStore()
	gw := newGateway(store, time.Unix(100, 0))

	results, err := gw.PushMutations(context.Background(), workspaceA, "user_x", nil)
	if err != nil {
		t.Fatalf("PushMutations: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("want no results, got %+v", results)
	}
}

// TestPushMutations_WithinTxErrorPropagates proves an error returned by
// WithinTx itself (not by the Store methods within it) still surfaces.
func TestPushMutations_WithinTxErrorPropagates(t *testing.T) {
	boom := errors.New("boom")
	gw := &Gateway{
		WithinTx: func(context.Context, func(context.Context, Store) error) error { return boom },
		Now:      func() time.Time { return time.Unix(1, 0) },
	}
	_, err := gw.PushMutations(context.Background(), workspaceA, "user_x", []*syncv1.Mutation{setTitleMutation("m1", "x")})
	if !errors.Is(err, boom) {
		t.Fatalf("want boom wrapped, got %v", err)
	}
}

// TestOpName_Unknown covers opName's defensive default branch, which
// applyOne can never reach in practice (decodeOp already rejects any
// mutation with no recognized op before opName is called).
func TestOpName_Unknown(t *testing.T) {
	if got := opName(&syncv1.Mutation{}); got != "unknown" {
		t.Fatalf("opName(no-op mutation) = %q, want %q", got, "unknown")
	}
}

// TestSetDocField_CorruptExistingDoc covers setDocField's one failure
// mode: an existing doc that isn't valid JSON.
func TestSetDocField_CorruptExistingDoc(t *testing.T) {
	_, err := setDocField([]byte("not json"), fieldTitle, "x")
	if err == nil {
		t.Fatalf("want error for corrupt existing doc, got nil")
	}
}

// TestGateway_DefaultNow proves the zero-value Gateway.Now falls back to
// wall-clock time rather than panicking.
func TestGateway_DefaultNow(t *testing.T) {
	store := newFakeStore()
	seedCapture(store, workspaceA)
	gw := &Gateway{WithinTx: withinTx(store)}

	before := time.Now()
	results, err := gw.PushMutations(context.Background(), workspaceA, "user_x", []*syncv1.Mutation{setTitleMutation("m1", "x")})
	after := time.Now()
	if err != nil {
		t.Fatalf("PushMutations: %v", err)
	}
	if !results[0].Applied {
		t.Fatalf("want applied, got %+v", results[0])
	}
	v := store.fieldVersions[store.key(captureID, fieldTitle)]
	if v.ServerT.Before(before) || v.ServerT.After(after) {
		t.Fatalf("default Now() should be real wall-clock time, got %v (window %v..%v)", v.ServerT, before, after)
	}
}
