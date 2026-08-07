package handler

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"

	syncv1 "github.com/dhiazfathra/how-to-replicate/proto/gen/go/sync/v1"
	"github.com/dhiazfathra/how-to-replicate/services/sync-gateway/internal/authctx"
	"github.com/dhiazfathra/how-to-replicate/services/sync-gateway/internal/gateway"
)

// stubStore is the minimal gateway.Store fake needed to drive the handler
// end to end without a database.
type stubStore struct {
	captures map[string]gateway.Capture
	seen     map[string]bool
}

func newStubStore() *stubStore {
	return &stubStore{captures: map[string]gateway.Capture{}, seen: map[string]bool{}}
}

func (s *stubStore) LockCaptureForWorkspace(_ context.Context, captureID, workspaceID string) (gateway.Capture, bool, error) {
	c, ok := s.captures[captureID]
	if !ok || c.WorkspaceID != workspaceID {
		return gateway.Capture{}, false, nil
	}
	return c, true, nil
}

func (s *stubStore) InsertMutationIfNew(_ context.Context, mutationID, _, _ string, _ []byte, _ int64) (bool, error) {
	if s.seen[mutationID] {
		return false, nil
	}
	s.seen[mutationID] = true
	return true, nil
}

func (s *stubStore) FieldVersion(context.Context, string, string) (gateway.FieldVersion, bool, error) {
	return gateway.FieldVersion{}, false, nil
}

func (s *stubStore) SetFieldVersion(context.Context, string, string, gateway.FieldVersion) error {
	return nil
}

func (s *stubStore) UpdateCaptureRevisionAndDoc(_ context.Context, captureID string, revision int64, doc []byte) error {
	c := s.captures[captureID]
	c.Revision = revision
	c.Doc = doc
	s.captures[captureID] = c
	return nil
}

func (s *stubStore) InsertComment(context.Context, string, string, string, string) error { return nil }

func (s *stubStore) RecordSupersededMutation(context.Context, string, string, string, string) error {
	return nil
}

func newTestService(store *stubStore) *SyncService {
	gw := &gateway.Gateway{
		WithinTx: func(ctx context.Context, fn func(context.Context, gateway.Store) error) error {
			return fn(ctx, store)
		},
		Now: func() time.Time { return time.Unix(100, 0) },
	}
	return &SyncService{Gateway: gw}
}

func TestSyncService_PushMutations_Success(t *testing.T) {
	store := newStubStore()
	store.captures["cap_1"] = gateway.Capture{ID: "cap_1", WorkspaceID: "ws_1"}
	svc := newTestService(store)

	ctx := authctx.WithRole(authctx.WithWorkspaceID(context.Background(), "ws_1"), "member")
	req := connect.NewRequest(&syncv1.PushMutationsRequest{
		Mutations: []*syncv1.Mutation{
			{Id: "m1", CaptureId: "cap_1", Op: &syncv1.Mutation_SetTitle{SetTitle: &syncv1.SetTitle{Title: "x"}}},
		},
	})

	resp, err := svc.PushMutations(ctx, req)
	if err != nil {
		t.Fatalf("PushMutations: %v", err)
	}
	if len(resp.Msg.Results) != 1 || !resp.Msg.Results[0].Applied || resp.Msg.Results[0].Error != "" {
		t.Fatalf("want one applied result, got %+v", resp.Msg.Results)
	}
}

func TestSyncService_PushMutations_RejectionSurfacesAsTypedResult(t *testing.T) {
	store := newStubStore() // no captures seeded
	svc := newTestService(store)

	ctx := authctx.WithRole(authctx.WithWorkspaceID(context.Background(), "ws_1"), "member")
	req := connect.NewRequest(&syncv1.PushMutationsRequest{
		Mutations: []*syncv1.Mutation{
			{Id: "m1", CaptureId: "does_not_exist", Op: &syncv1.Mutation_SetTitle{SetTitle: &syncv1.SetTitle{Title: "x"}}},
		},
	})

	resp, err := svc.PushMutations(ctx, req)
	if err != nil {
		t.Fatalf("PushMutations should not transport-error on a rejection: %v", err)
	}
	if resp.Msg.Results[0].Applied || resp.Msg.Results[0].Error != string(gateway.ReasonNotFound) {
		t.Fatalf("want typed not_found rejection, got %+v", resp.Msg.Results[0])
	}
}

func TestSyncService_PushMutations_MissingWorkspaceIsUnauthenticated(t *testing.T) {
	svc := newTestService(newStubStore())

	req := connect.NewRequest(&syncv1.PushMutationsRequest{Mutations: []*syncv1.Mutation{}})
	_, err := svc.PushMutations(context.Background(), req)
	if err == nil {
		t.Fatalf("want error, got nil")
	}
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) || connectErr.Code() != connect.CodeUnauthenticated {
		t.Fatalf("want CodeUnauthenticated, got %v", err)
	}
}

func TestSyncService_PushMutations_MissingRoleIsPermissionDenied(t *testing.T) {
	svc := newTestService(newStubStore())

	ctx := authctx.WithWorkspaceID(context.Background(), "ws_1")
	req := connect.NewRequest(&syncv1.PushMutationsRequest{Mutations: []*syncv1.Mutation{}})
	_, err := svc.PushMutations(ctx, req)
	if err == nil {
		t.Fatalf("want error, got nil")
	}
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) || connectErr.Code() != connect.CodePermissionDenied {
		t.Fatalf("want CodePermissionDenied, got %v", err)
	}
}

func TestSyncService_PushMutations_ViewerRoleIsPermissionDenied(t *testing.T) {
	svc := newTestService(newStubStore())

	ctx := authctx.WithRole(authctx.WithWorkspaceID(context.Background(), "ws_1"), "viewer")
	req := connect.NewRequest(&syncv1.PushMutationsRequest{Mutations: []*syncv1.Mutation{}})
	_, err := svc.PushMutations(ctx, req)
	if err == nil {
		t.Fatalf("want error, got nil")
	}
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) || connectErr.Code() != connect.CodePermissionDenied {
		t.Fatalf("want CodePermissionDenied, got %v", err)
	}
}

func TestSyncService_PushMutations_GatewayErrorIsInternal(t *testing.T) {
	boom := errors.New("boom")
	gw := &gateway.Gateway{
		WithinTx: func(context.Context, func(context.Context, gateway.Store) error) error { return boom },
	}
	svc := &SyncService{Gateway: gw}

	ctx := authctx.WithRole(authctx.WithWorkspaceID(context.Background(), "ws_1"), "member")
	req := connect.NewRequest(&syncv1.PushMutationsRequest{
		Mutations: []*syncv1.Mutation{
			{Id: "m1", CaptureId: "cap_1", Op: &syncv1.Mutation_SetTitle{SetTitle: &syncv1.SetTitle{Title: "x"}}},
		},
	})

	_, err := svc.PushMutations(ctx, req)
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) || connectErr.Code() != connect.CodeInternal {
		t.Fatalf("want CodeInternal, got %v", err)
	}
}

// TestSyncService_UnimplementedRPCs proves the RPCs owned by Tasks 5/6
// are explicitly unimplemented, not silently missing.
func TestSyncService_UnimplementedRPCs(t *testing.T) {
	svc := &SyncService{}
	ctx := context.Background()

	if _, err := svc.PullDeltas(ctx, connect.NewRequest(&syncv1.PullDeltasRequest{})); err == nil {
		t.Fatalf("want PullDeltas unimplemented error")
	}
	if _, err := svc.RequestAssetUpload(ctx, connect.NewRequest(&syncv1.RequestAssetUploadRequest{})); err == nil {
		t.Fatalf("want RequestAssetUpload unimplemented error")
	}
	if _, err := svc.CompleteAssetUpload(ctx, connect.NewRequest(&syncv1.CompleteAssetUploadRequest{})); err == nil {
		t.Fatalf("want CompleteAssetUpload unimplemented error")
	}
}
