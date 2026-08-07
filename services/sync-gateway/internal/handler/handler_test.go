package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
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
	captures          map[string]gateway.Capture
	seen              map[string]bool
	assets            map[string]gateway.Asset
	presigns          map[string]gateway.AssetUploadPresign
	consumedPresigns  map[string]bool
	manifestComplete  map[string]bool
	deltaLog          []gateway.DeltaMutation
	deltaLogWorkspace []string
}

func newStubStore() *stubStore {
	return &stubStore{
		captures:         map[string]gateway.Capture{},
		seen:             map[string]bool{},
		assets:           map[string]gateway.Asset{},
		presigns:         map[string]gateway.AssetUploadPresign{},
		consumedPresigns: map[string]bool{},
		manifestComplete: map[string]bool{},
	}
}

func (s *stubStore) GetAssetForWorkspace(_ context.Context, assetID, captureID, workspaceID string) (gateway.Asset, bool, error) {
	a, ok := s.assets[assetID]
	if !ok || a.CaptureID != captureID {
		return gateway.Asset{}, false, nil
	}
	c, ok := s.captures[captureID]
	if !ok || c.WorkspaceID != workspaceID {
		return gateway.Asset{}, false, nil
	}
	return a, true, nil
}

func (s *stubStore) UpsertAssetForUpload(_ context.Context, asset gateway.Asset) (gateway.Asset, error) {
	existing, ok := s.assets[asset.ID]
	if ok {
		existing.ObjectKey = asset.ObjectKey
		s.assets[asset.ID] = existing
		return existing, nil
	}
	s.assets[asset.ID] = asset
	return asset, nil
}

func (s *stubStore) CreateAssetUploadPresign(_ context.Context, p gateway.AssetUploadPresign) error {
	s.presigns[p.ObjectKey] = p
	return nil
}

func (s *stubStore) GetAssetUploadPresignByKey(_ context.Context, objectKey string) (gateway.AssetUploadPresign, bool, error) {
	p, ok := s.presigns[objectKey]
	return p, ok, nil
}

func (s *stubStore) ConsumeAssetUploadPresign(_ context.Context, objectKey string) (bool, error) {
	if _, ok := s.presigns[objectKey]; !ok {
		return false, nil
	}
	if s.consumedPresigns[objectKey] {
		return false, nil
	}
	s.consumedPresigns[objectKey] = true
	return true, nil
}

func (s *stubStore) MarkAssetVerified(_ context.Context, assetID, sha256Hex string, sizeBytes int64) error {
	a := s.assets[assetID]
	a.Sha256 = sha256Hex
	a.SizeBytes = sizeBytes
	a.Verified = true
	s.assets[assetID] = a
	return nil
}

func (s *stubStore) ManifestComplete(_ context.Context, captureID string) (bool, error) {
	total, unverified := 0, 0
	for _, a := range s.assets {
		if a.CaptureID != captureID {
			continue
		}
		total++
		if !a.Verified {
			unverified++
		}
	}
	return total > 0 && unverified == 0, nil
}

func (s *stubStore) SetCaptureManifestComplete(_ context.Context, captureID string, complete bool) error {
	s.manifestComplete[captureID] = complete
	return nil
}

// stubObjectStore is a minimal in-memory gateway.ObjectStore.
type stubObjectStore struct {
	objects map[string][]byte
}

func newStubObjectStore() *stubObjectStore {
	return &stubObjectStore{objects: map[string][]byte{}}
}

func (o *stubObjectStore) PresignPutChecksummed(_ context.Context, key string, _ time.Duration, sha256Hex string) (*url.URL, map[string]string, error) {
	u, _ := url.Parse("https://minio.example/" + key)
	return u, map[string]string{"x-amz-checksum-sha256": sha256Hex, "If-None-Match": "*"}, nil
}

func (o *stubObjectStore) StatSize(_ context.Context, key string) (bool, int64, error) {
	b, ok := o.objects[key]
	if !ok {
		return false, 0, nil
	}
	return true, int64(len(b)), nil
}

func (o *stubObjectStore) HashObject(_ context.Context, key string) (string, error) {
	b, ok := o.objects[key]
	if !ok {
		return "", errors.New("stubObjectStore: no such object")
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func sha256HexOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (s *stubStore) LockCaptureForWorkspace(_ context.Context, captureID, workspaceID string) (gateway.Capture, bool, error) {
	c, ok := s.captures[captureID]
	if !ok || c.WorkspaceID != workspaceID {
		return gateway.Capture{}, false, nil
	}
	return c, true, nil
}

func (s *stubStore) InsertMutationIfNew(_ context.Context, mutationID, captureID, workspaceID, op string, payload []byte, clientT int64) (bool, error) {
	if s.seen[mutationID] {
		return false, nil
	}
	s.seen[mutationID] = true
	s.deltaLog = append(s.deltaLog, gateway.DeltaMutation{
		Seq: int64(len(s.deltaLog) + 1), ID: mutationID, CaptureID: captureID,
		Op: op, Payload: payload, ClientT: clientT,
	})
	s.deltaLogWorkspace = append(s.deltaLogWorkspace, workspaceID)
	return true, nil
}

// PullMutationsSince mirrors gateway_test.go's fakeStore implementation —
// see that file for the reasoning.
func (s *stubStore) PullMutationsSince(_ context.Context, workspaceID string, since int64, limit int32) ([]gateway.DeltaMutation, error) {
	out := make([]gateway.DeltaMutation, 0)
	for i, d := range s.deltaLog {
		if s.deltaLogWorkspace[i] != workspaceID || d.Seq <= since {
			continue
		}
		out = append(out, d)
		if int32(len(out)) >= limit { //nolint:gosec // test fixture, len(out) bounded by test data size
			break
		}
	}
	return out, nil
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
	return newTestServiceWithStorage(store, newStubObjectStore())
}

func newTestServiceWithStorage(store *stubStore, objStore gateway.ObjectStore) *SyncService {
	gw := &gateway.Gateway{
		WithinTx: func(ctx context.Context, fn func(context.Context, gateway.Store) error) error {
			return fn(ctx, store)
		},
		Now:     func() time.Time { return time.Unix(100, 0) },
		Storage: objStore,
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

// TestSyncService_PullDeltas_Success exercises the full happy path: push a
// few mutations, then pull them back through the RPC surface and confirm
// the wire response carries every one, in order, with the revision cursor
// advanced.
func TestSyncService_PullDeltas_Success(t *testing.T) {
	store := newStubStore()
	store.captures["cap_1"] = gateway.Capture{ID: "cap_1", WorkspaceID: "ws_1"}
	svc := newTestService(store)
	ctx := authctx.WithRole(authctx.WithWorkspaceID(context.Background(), "ws_1"), "member")

	if _, err := svc.PushMutations(ctx, connect.NewRequest(&syncv1.PushMutationsRequest{
		Mutations: []*syncv1.Mutation{
			{Id: "m1", CaptureId: "cap_1", Op: &syncv1.Mutation_SetTitle{SetTitle: &syncv1.SetTitle{Title: "x"}}},
			{Id: "m2", CaptureId: "cap_1", Op: &syncv1.Mutation_SetTitle{SetTitle: &syncv1.SetTitle{Title: "y"}}},
		},
	})); err != nil {
		t.Fatalf("PushMutations: %v", err)
	}

	resp, err := svc.PullDeltas(ctx, connect.NewRequest(&syncv1.PullDeltasRequest{Since: 0}))
	if err != nil {
		t.Fatalf("PullDeltas: %v", err)
	}
	if len(resp.Msg.Mutations) != 2 || resp.Msg.Mutations[0].GetId() != "m1" || resp.Msg.Mutations[1].GetId() != "m2" {
		t.Fatalf("want [m1 m2], got %+v", resp.Msg.Mutations)
	}
	if resp.Msg.Revision != 2 || resp.Msg.HasMore {
		t.Fatalf("want revision=2 hasMore=false, got revision=%d hasMore=%v", resp.Msg.Revision, resp.Msg.HasMore)
	}

	// since= the current head returns nothing new.
	empty, err := svc.PullDeltas(ctx, connect.NewRequest(&syncv1.PullDeltasRequest{Since: resp.Msg.Revision}))
	if err != nil {
		t.Fatalf("PullDeltas at head: %v", err)
	}
	if len(empty.Msg.Mutations) != 0 {
		t.Fatalf("want no deltas at head, got %+v", empty.Msg.Mutations)
	}
}

// TestSyncService_PullDeltas_WorkspaceIdMustMatchAuthenticatedWorkspace
// proves the request body's workspace_id can never be used to read a
// workspace other than the one the caller's own credential scopes it to —
// a client cannot use the field to escape workspace isolation.
func TestSyncService_PullDeltas_WorkspaceIdMustMatchAuthenticatedWorkspace(t *testing.T) {
	svc := newTestService(newStubStore())
	ctx := authctx.WithRole(authctx.WithWorkspaceID(context.Background(), "ws_1"), "member")

	_, err := svc.PullDeltas(ctx, connect.NewRequest(&syncv1.PullDeltasRequest{WorkspaceId: "ws_other"}))
	if err == nil {
		t.Fatalf("want error, got nil")
	}
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) || connectErr.Code() != connect.CodePermissionDenied {
		t.Fatalf("want CodePermissionDenied, got %v", err)
	}
}

// TestSyncService_PullDeltas_MatchingWorkspaceIdIsAllowed proves the
// workspace_id field is fine to send as long as it agrees with the
// authenticated workspace — the mismatch check must not reject the normal
// case.
func TestSyncService_PullDeltas_MatchingWorkspaceIdIsAllowed(t *testing.T) {
	svc := newTestService(newStubStore())
	ctx := authctx.WithRole(authctx.WithWorkspaceID(context.Background(), "ws_1"), "member")

	resp, err := svc.PullDeltas(ctx, connect.NewRequest(&syncv1.PullDeltasRequest{WorkspaceId: "ws_1"}))
	if err != nil {
		t.Fatalf("PullDeltas: %v", err)
	}
	if len(resp.Msg.Mutations) != 0 {
		t.Fatalf("want no mutations, got %+v", resp.Msg.Mutations)
	}
}

func TestSyncService_PullDeltas_MissingWorkspaceIsUnauthenticated(t *testing.T) {
	svc := newTestService(newStubStore())

	_, err := svc.PullDeltas(context.Background(), connect.NewRequest(&syncv1.PullDeltasRequest{}))
	if err == nil {
		t.Fatalf("want error, got nil")
	}
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) || connectErr.Code() != connect.CodeUnauthenticated {
		t.Fatalf("want CodeUnauthenticated, got %v", err)
	}
}

func TestSyncService_PullDeltas_ViewerRoleIsAllowed(t *testing.T) {
	svc := newTestService(newStubStore())
	ctx := authctx.WithRole(authctx.WithWorkspaceID(context.Background(), "ws_1"), "viewer")

	if _, err := svc.PullDeltas(ctx, connect.NewRequest(&syncv1.PullDeltasRequest{})); err != nil {
		t.Fatalf("PullDeltas: viewer should have capture:read, got %v", err)
	}
}

func TestSyncService_PullDeltas_MissingRoleIsPermissionDenied(t *testing.T) {
	svc := newTestService(newStubStore())
	ctx := authctx.WithWorkspaceID(context.Background(), "ws_1")

	_, err := svc.PullDeltas(ctx, connect.NewRequest(&syncv1.PullDeltasRequest{}))
	if err == nil {
		t.Fatalf("want error, got nil")
	}
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) || connectErr.Code() != connect.CodePermissionDenied {
		t.Fatalf("want CodePermissionDenied, got %v", err)
	}
}

func TestSyncService_PullDeltas_GatewayErrorIsInternal(t *testing.T) {
	boom := errors.New("boom")
	gw := &gateway.Gateway{
		WithinTx: func(context.Context, func(context.Context, gateway.Store) error) error { return boom },
	}
	svc := &SyncService{Gateway: gw}
	ctx := authctx.WithRole(authctx.WithWorkspaceID(context.Background(), "ws_1"), "member")

	_, err := svc.PullDeltas(ctx, connect.NewRequest(&syncv1.PullDeltasRequest{}))
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) || connectErr.Code() != connect.CodeInternal {
		t.Fatalf("want CodeInternal, got %v", err)
	}
}

func assetUploadCtx() context.Context {
	return authctx.WithRole(authctx.WithWorkspaceID(context.Background(), "ws_1"), "member")
}

func TestSyncService_RequestAssetUpload_Success(t *testing.T) {
	store := newStubStore()
	store.captures["cap_1"] = gateway.Capture{ID: "cap_1", WorkspaceID: "ws_1"}
	svc := newTestService(store)

	body := []byte("video bytes")
	req := connect.NewRequest(&syncv1.RequestAssetUploadRequest{
		CaptureId: "cap_1",
		AssetId:   "asset_1",
		MimeType:  "video/mp4",
		SizeBytes: int64(len(body)),
		Sha256:    sha256HexOf(body),
	})

	resp, err := svc.RequestAssetUpload(assetUploadCtx(), req)
	if err != nil {
		t.Fatalf("RequestAssetUpload: %v", err)
	}
	if resp.Msg.GetUploadUrl() == "" || resp.Msg.GetObjectKey() == "" {
		t.Fatalf("expected populated upload url and object key, got %+v", resp.Msg)
	}
	if resp.Msg.GetRequiredHeaders()["If-None-Match"] != "*" {
		t.Fatalf("expected If-None-Match required header, got %v", resp.Msg.GetRequiredHeaders())
	}
	if resp.Msg.GetExpiresAtUnixMs() <= 0 {
		t.Fatalf("expected positive expiry, got %d", resp.Msg.GetExpiresAtUnixMs())
	}
}

func TestSyncService_RequestAssetUpload_NotFoundIsTransportError(t *testing.T) {
	svc := newTestService(newStubStore())

	req := connect.NewRequest(&syncv1.RequestAssetUploadRequest{
		CaptureId: "does_not_exist",
		AssetId:   "asset_1",
		MimeType:  "video/mp4",
		SizeBytes: 10,
		Sha256:    sha256HexOf([]byte("x")),
	})
	_, err := svc.RequestAssetUpload(assetUploadCtx(), req)
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) || connectErr.Code() != connect.CodeNotFound {
		t.Fatalf("want CodeNotFound, got %v", err)
	}
}

func TestSyncService_RequestAssetUpload_InvalidRequestIsTransportError(t *testing.T) {
	store := newStubStore()
	store.captures["cap_1"] = gateway.Capture{ID: "cap_1", WorkspaceID: "ws_1"}
	svc := newTestService(store)

	req := connect.NewRequest(&syncv1.RequestAssetUploadRequest{
		CaptureId: "cap_1",
		AssetId:   "asset_1",
		MimeType:  "video/mp4",
		SizeBytes: 0, // invalid
		Sha256:    sha256HexOf([]byte("x")),
	})
	_, err := svc.RequestAssetUpload(assetUploadCtx(), req)
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) || connectErr.Code() != connect.CodeInvalidArgument {
		t.Fatalf("want CodeInvalidArgument, got %v", err)
	}
}

func TestSyncService_RequestAssetUpload_MissingWorkspaceIsUnauthenticated(t *testing.T) {
	svc := newTestService(newStubStore())
	_, err := svc.RequestAssetUpload(context.Background(), connect.NewRequest(&syncv1.RequestAssetUploadRequest{}))
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) || connectErr.Code() != connect.CodeUnauthenticated {
		t.Fatalf("want CodeUnauthenticated, got %v", err)
	}
}

func TestSyncService_RequestAssetUpload_MissingRoleIsPermissionDenied(t *testing.T) {
	svc := newTestService(newStubStore())
	ctx := authctx.WithWorkspaceID(context.Background(), "ws_1")
	_, err := svc.RequestAssetUpload(ctx, connect.NewRequest(&syncv1.RequestAssetUploadRequest{}))
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) || connectErr.Code() != connect.CodePermissionDenied {
		t.Fatalf("want CodePermissionDenied, got %v", err)
	}
}

func TestSyncService_CompleteAssetUpload_HappyPath(t *testing.T) {
	store := newStubStore()
	store.captures["cap_1"] = gateway.Capture{ID: "cap_1", WorkspaceID: "ws_1"}
	objStore := newStubObjectStore()
	svc := newTestServiceWithStorage(store, objStore)

	body := []byte("video bytes")
	reqUp := connect.NewRequest(&syncv1.RequestAssetUploadRequest{
		CaptureId: "cap_1", AssetId: "asset_1", MimeType: "video/mp4",
		SizeBytes: int64(len(body)), Sha256: sha256HexOf(body),
	})
	respUp, err := svc.RequestAssetUpload(assetUploadCtx(), reqUp)
	if err != nil {
		t.Fatalf("RequestAssetUpload: %v", err)
	}
	objStore.objects[respUp.Msg.GetObjectKey()] = body

	resp, err := svc.CompleteAssetUpload(assetUploadCtx(), connect.NewRequest(&syncv1.CompleteAssetUploadRequest{
		CaptureId: "cap_1", AssetId: "asset_1",
	}))
	if err != nil {
		t.Fatalf("CompleteAssetUpload: %v", err)
	}
	if !resp.Msg.GetVerified() || !resp.Msg.GetManifestComplete() || resp.Msg.GetError() != "" {
		t.Fatalf("expected verified+complete+no error, got %+v", resp.Msg)
	}
}

func TestSyncService_CompleteAssetUpload_MismatchSurfacesTypedError(t *testing.T) {
	store := newStubStore()
	store.captures["cap_1"] = gateway.Capture{ID: "cap_1", WorkspaceID: "ws_1"}
	objStore := newStubObjectStore()
	svc := newTestServiceWithStorage(store, objStore)

	respUp, err := svc.RequestAssetUpload(assetUploadCtx(), connect.NewRequest(&syncv1.RequestAssetUploadRequest{
		CaptureId: "cap_1", AssetId: "asset_1", MimeType: "video/mp4",
		SizeBytes: 5, Sha256: sha256HexOf([]byte("hello")),
	}))
	if err != nil {
		t.Fatalf("RequestAssetUpload: %v", err)
	}
	objStore.objects[respUp.Msg.GetObjectKey()] = []byte("wrong-size-body")

	resp, err := svc.CompleteAssetUpload(assetUploadCtx(), connect.NewRequest(&syncv1.CompleteAssetUploadRequest{
		CaptureId: "cap_1", AssetId: "asset_1",
	}))
	if err != nil {
		t.Fatalf("CompleteAssetUpload should not transport-error on a mismatch: %v", err)
	}
	if resp.Msg.GetVerified() || resp.Msg.GetManifestComplete() || resp.Msg.GetError() != string(gateway.AssetReasonSizeMismatch) {
		t.Fatalf("expected typed size_mismatch rejection, got %+v", resp.Msg)
	}
}

func TestSyncService_CompleteAssetUpload_MissingWorkspaceIsUnauthenticated(t *testing.T) {
	svc := newTestService(newStubStore())
	_, err := svc.CompleteAssetUpload(context.Background(), connect.NewRequest(&syncv1.CompleteAssetUploadRequest{}))
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) || connectErr.Code() != connect.CodeUnauthenticated {
		t.Fatalf("want CodeUnauthenticated, got %v", err)
	}
}

func TestSyncService_CompleteAssetUpload_MissingRoleIsPermissionDenied(t *testing.T) {
	svc := newTestService(newStubStore())
	ctx := authctx.WithWorkspaceID(context.Background(), "ws_1")
	_, err := svc.CompleteAssetUpload(ctx, connect.NewRequest(&syncv1.CompleteAssetUploadRequest{}))
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) || connectErr.Code() != connect.CodePermissionDenied {
		t.Fatalf("want CodePermissionDenied, got %v", err)
	}
}
