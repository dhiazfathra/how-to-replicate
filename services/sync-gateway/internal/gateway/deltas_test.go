package gateway

import (
	"context"
	"errors"
	"testing"
	"time"

	syncv1 "github.com/dhiazfathra/how-to-replicate/proto/gen/go/sync/v1"
)

func tagsMutation(id string, tags []string) *syncv1.Mutation {
	return &syncv1.Mutation{
		Id:        id,
		CaptureId: captureID,
		Op:        &syncv1.Mutation_SetTags{SetTags: &syncv1.SetTags{Tags: tags}},
	}
}

func assignMutation(id, assignee string) *syncv1.Mutation {
	return &syncv1.Mutation{
		Id:        id,
		CaptureId: captureID,
		Op:        &syncv1.Mutation_Assign{Assign: &syncv1.Assign{AssigneeUserId: assignee}},
	}
}

func commentMutation(id, commentID, body string) *syncv1.Mutation {
	return &syncv1.Mutation{
		Id:        id,
		CaptureId: captureID,
		Op: &syncv1.Mutation_AppendComment{AppendComment: &syncv1.AppendComment{
			CommentId: commentID,
			Body:      body,
		}},
	}
}

func summaryMutation(id, summary string) *syncv1.Mutation {
	return &syncv1.Mutation{
		Id:        id,
		CaptureId: captureID,
		Op:        &syncv1.Mutation_SetSummary{SetSummary: &syncv1.SetSummary{Summary: summary}},
	}
}

// TestOnCommit_CalledOnceOnNewDeltaOnlyNotOnReplay proves the Task 6
// fan-out hook fires exactly for durable new writes: once per accepted
// mutation, and not again when the identical mutation ID is redelivered
// (ADR-012's idempotent-replay path produces no new delta, so nothing new
// exists for a subscriber to hear about).
func TestOnCommit_CalledOnceOnNewDeltaOnlyNotOnReplay(t *testing.T) {
	store := newFakeStore()
	seedCapture(store, workspaceA)
	gw := newGateway(store, time.Unix(0, 0))
	var notified []string
	gw.OnCommit = func(workspaceID string) { notified = append(notified, workspaceID) }
	ctx := context.Background()

	if _, err := gw.PushMutations(ctx, workspaceA, "actor", []*syncv1.Mutation{setTitleMutation("m1", "a")}); err != nil {
		t.Fatalf("push: %v", err)
	}
	if len(notified) != 1 || notified[0] != workspaceA {
		t.Fatalf("want one OnCommit(%q), got %+v", workspaceA, notified)
	}

	// Replay of the same mutation ID must not notify again.
	if _, err := gw.PushMutations(ctx, workspaceA, "actor", []*syncv1.Mutation{setTitleMutation("m1", "a")}); err != nil {
		t.Fatalf("replay push: %v", err)
	}
	if len(notified) != 1 {
		t.Fatalf("want no additional OnCommit on replay, got %+v", notified)
	}

	// A rejected mutation (unknown capture) must not notify either.
	unknownCapture := &syncv1.Mutation{
		Id:        "m2",
		CaptureId: "does_not_exist",
		Op:        &syncv1.Mutation_SetTitle{SetTitle: &syncv1.SetTitle{Title: "b"}},
	}
	if _, err := gw.PushMutations(ctx, workspaceA, "actor", []*syncv1.Mutation{unknownCapture}); err != nil {
		t.Fatalf("push to unknown capture: %v", err)
	}
	if len(notified) != 1 {
		t.Fatalf("want no OnCommit for a rejected mutation, got %+v", notified)
	}
}

// TestPullDeltas_RoundTripsEveryOpKind pushes one mutation of each closed op
// kind (spec's five ops), then pulls the whole delta log back and asserts
// every field decodes exactly as it was written — the coverage decodePayload
// needs since it is the exact reverse of decodeOp/opName.
func TestPullDeltas_RoundTripsEveryOpKind(t *testing.T) {
	store := newFakeStore()
	seedCapture(store, workspaceA)
	gw := newGateway(store, time.Unix(0, 0))
	ctx := context.Background()

	muts := []*syncv1.Mutation{
		setTitleMutation("m1", "hello"),
		summaryMutation("m2", "a summary"),
		tagsMutation("m3", []string{"a", "b"}),
		assignMutation("m4", "user_9"),
		commentMutation("m5", "c1", "nice work"),
	}
	if _, err := gw.PushMutations(ctx, workspaceA, "actor", muts); err != nil {
		t.Fatalf("PushMutations: %v", err)
	}

	got, revision, hasMore, err := gw.PullDeltas(ctx, workspaceA, 0, 0)
	if err != nil {
		t.Fatalf("PullDeltas: %v", err)
	}
	if hasMore {
		t.Fatalf("want hasMore=false, got true")
	}
	if revision != 5 {
		t.Fatalf("want revision=5, got %d", revision)
	}
	if len(got) != 5 {
		t.Fatalf("want 5 deltas, got %d", len(got))
	}

	if got[0].GetId() != "m1" || got[0].GetSetTitle().GetTitle() != "hello" {
		t.Fatalf("set_title mismatch: %+v", got[0])
	}
	if got[1].GetId() != "m2" || got[1].GetSetSummary().GetSummary() != "a summary" {
		t.Fatalf("set_summary mismatch: %+v", got[1])
	}
	tags := got[2].GetSetTags().GetTags()
	if got[2].GetId() != "m3" || len(tags) != 2 || tags[0] != "a" || tags[1] != "b" {
		t.Fatalf("set_tags mismatch: %+v", got[2])
	}
	if got[3].GetId() != "m4" || got[3].GetAssign().GetAssigneeUserId() != "user_9" {
		t.Fatalf("assign mismatch: %+v", got[3])
	}
	if got[4].GetId() != "m5" || got[4].GetAppendComment().GetCommentId() != "c1" || got[4].GetAppendComment().GetBody() != "nice work" {
		t.Fatalf("append_comment mismatch: %+v", got[4])
	}
	for _, m := range got {
		if m.GetCaptureId() != captureID {
			t.Fatalf("want capture id %q on every delta, got %+v", captureID, m)
		}
	}
}

// TestPullDeltas_SinceExcludesAlreadySeen is the core "delta not snapshot"
// behavior: pulling since the current revision returns nothing, and pulling
// since a lower revision returns only what's above it.
func TestPullDeltas_SinceExcludesAlreadySeen(t *testing.T) {
	store := newFakeStore()
	seedCapture(store, workspaceA)
	gw := newGateway(store, time.Unix(0, 0))
	ctx := context.Background()

	for i, id := range []string{"m1", "m2", "m3"} {
		if _, err := gw.PushMutations(ctx, workspaceA, "actor", []*syncv1.Mutation{setTitleMutation(id, id)}); err != nil {
			t.Fatalf("push %d: %v", i, err)
		}
	}

	got, revision, _, err := gw.PullDeltas(ctx, workspaceA, 1, 0)
	if err != nil {
		t.Fatalf("PullDeltas: %v", err)
	}
	if len(got) != 2 || got[0].GetId() != "m2" || got[1].GetId() != "m3" {
		t.Fatalf("want [m2 m3], got %+v", got)
	}
	if revision != 3 {
		t.Fatalf("want revision 3, got %d", revision)
	}

	if got, _, _, err := gw.PullDeltas(ctx, workspaceA, 3, 0); err != nil || len(got) != 0 {
		t.Fatalf("want empty pull at head, got %+v err=%v", got, err)
	}
}

// TestPullDeltas_WorkspaceIsolation proves a workspace's PullDeltas call
// never returns another workspace's mutations, mirroring the fan-out's
// workspace-scoping requirement at the pull layer.
func TestPullDeltas_WorkspaceIsolation(t *testing.T) {
	store := newFakeStore()
	store.captures["cap_a"] = Capture{ID: "cap_a", WorkspaceID: workspaceA}
	store.captures["cap_b"] = Capture{ID: "cap_b", WorkspaceID: workspaceB}
	gw := newGateway(store, time.Unix(0, 0))
	ctx := context.Background()

	push := func(ws, capID, id string) {
		t.Helper()
		m := &syncv1.Mutation{Id: id, CaptureId: capID, Op: &syncv1.Mutation_SetTitle{SetTitle: &syncv1.SetTitle{Title: id}}}
		if _, err := gw.PushMutations(ctx, ws, "actor", []*syncv1.Mutation{m}); err != nil {
			t.Fatalf("push: %v", err)
		}
	}
	push(workspaceA, "cap_a", "a1")
	push(workspaceB, "cap_b", "b1")
	push(workspaceA, "cap_a", "a2")

	got, _, _, err := gw.PullDeltas(ctx, workspaceA, 0, 0)
	if err != nil {
		t.Fatalf("PullDeltas: %v", err)
	}
	if len(got) != 2 || got[0].GetId() != "a1" || got[1].GetId() != "a2" {
		t.Fatalf("want only workspaceA's deltas [a1 a2], got %+v", got)
	}
}

// TestPullDeltas_Pagination exercises has_more and the resume cursor: a
// caller that keeps calling with since=<returned revision> eventually sees
// hasMore=false and has visited every mutation exactly once.
func TestPullDeltas_Pagination(t *testing.T) {
	store := newFakeStore()
	seedCapture(store, workspaceA)
	gw := newGateway(store, time.Unix(0, 0))
	ctx := context.Background()

	const total = 7
	for i := range total {
		id := string(rune('a' + i))
		if _, err := gw.PushMutations(ctx, workspaceA, "actor", []*syncv1.Mutation{setTitleMutation(id, id)}); err != nil {
			t.Fatalf("push: %v", err)
		}
	}

	var since int64
	seen := map[string]bool{}
	for pages := 0; ; pages++ {
		if pages > total {
			t.Fatalf("pagination did not terminate")
		}
		muts, revision, hasMore, err := gw.PullDeltas(ctx, workspaceA, since, 3)
		if err != nil {
			t.Fatalf("PullDeltas: %v", err)
		}
		for _, m := range muts {
			seen[m.GetId()] = true
		}
		since = revision
		if !hasMore {
			break
		}
	}
	if len(seen) != total {
		t.Fatalf("want %d distinct mutations visited, got %d: %+v", total, len(seen), seen)
	}
}

// TestPullDeltas_StoreError proves an infra failure from the store surfaces
// as an error rather than an empty/partial result.
func TestPullDeltas_StoreError(t *testing.T) {
	store := newFakeStore()
	store.pullErr = errors.New("boom")
	gw := newGateway(store, time.Unix(0, 0))

	if _, _, _, err := gw.PullDeltas(context.Background(), workspaceA, 0, 0); err == nil {
		t.Fatalf("want error, got nil")
	}
}

// TestPullDeltas_DecodeErrorSurfaces proves a corrupt row in the delta log
// (e.g. from a future op the running binary predates) makes PullDeltas
// return an error rather than a truncated or corrupt-but-successful result.
func TestPullDeltas_DecodeErrorSurfaces(t *testing.T) {
	store := newFakeStore()
	store.deltaLog = append(store.deltaLog, DeltaMutation{Seq: 1, ID: "bad", CaptureID: captureID, Op: "not_a_real_op", Payload: []byte(`{}`)})
	store.deltaLogWorkspace = append(store.deltaLogWorkspace, workspaceA)
	gw := newGateway(store, time.Unix(0, 0))

	if _, _, _, err := gw.PullDeltas(context.Background(), workspaceA, 0, 0); err == nil {
		t.Fatalf("want error for an undecodable delta row, got nil")
	}
}

// TestDecodePayload_UnknownOp proves a corrupt/unknown op in the delta log
// is reported as an error rather than silently dropped or panicking.
func TestDecodePayload_UnknownOp(t *testing.T) {
	if _, err := decodePayload("not_a_real_op", []byte(`{}`)); err == nil {
		t.Fatalf("want error for unknown op, got nil")
	}
}

// TestDecodePayload_MalformedPayload proves a malformed JSON payload for a
// known op is reported as an error for every op kind, not just one.
func TestDecodePayload_MalformedPayload(t *testing.T) {
	for _, op := range []string{"set_title", "set_summary", "set_tags", "assign", "append_comment"} {
		if _, err := decodePayload(op, []byte(`not json`)); err == nil {
			t.Fatalf("op %q: want error for malformed payload, got nil", op)
		}
	}
}
