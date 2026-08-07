// Package handler adapts sync-gateway's ConnectRPC surface to
// internal/gateway. PushMutations is implemented here; PullDeltas,
// RequestAssetUpload, and CompleteAssetUpload belong to Tasks 5 and 6 and
// return Unimplemented until then.
package handler

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	syncv1 "github.com/dhiazfathra/how-to-replicate/proto/gen/go/sync/v1"
	"github.com/dhiazfathra/how-to-replicate/proto/gen/go/sync/v1/syncv1connect"
	"github.com/dhiazfathra/how-to-replicate/services/internal/authz"
	"github.com/dhiazfathra/how-to-replicate/services/sync-gateway/internal/authctx"
	"github.com/dhiazfathra/how-to-replicate/services/sync-gateway/internal/gateway"
)

// SyncService implements syncv1connect.SyncServiceHandler's PushMutations
// against a gateway.Gateway. The other three RPCs are unimplemented here
// (Tasks 5/6).
type SyncService struct {
	syncv1connect.UnimplementedSyncServiceHandler

	Gateway *gateway.Gateway
}

var _ syncv1connect.SyncServiceHandler = (*SyncService)(nil)

// PushMutations requires a workspace ID in the request context (see
// internal/authctx) and rejects the whole request with Unauthenticated if
// one isn't present. Per-mutation outcomes — including cross-workspace or
// unknown-capture rejection — are reported in the response body, never as
// a transport error, so the client's rollback rule can key on the explicit
// per-mutation result.
func (s *SyncService) PushMutations(
	ctx context.Context,
	req *connect.Request[syncv1.PushMutationsRequest],
) (*connect.Response[syncv1.PushMutationsResponse], error) {
	workspaceID, ok := authctx.WorkspaceID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errUnauthenticated)
	}
	role, _ := authctx.Role(ctx)
	if !authz.Can(authz.Role(role), authz.PermissionCaptureWrite) {
		return nil, connect.NewError(connect.CodePermissionDenied, errPermissionDenied)
	}
	actorID, _ := authctx.UserID(ctx)

	results, err := s.Gateway.PushMutations(ctx, workspaceID, actorID, req.Msg.GetMutations())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	resp := &syncv1.PushMutationsResponse{Results: make([]*syncv1.PushMutationsResult, 0, len(results))}
	for _, r := range results {
		resp.Results = append(resp.Results, &syncv1.PushMutationsResult{
			MutationId: r.MutationID,
			Applied:    r.Applied,
			Error:      string(r.Reason),
		})
	}
	return connect.NewResponse(resp), nil
}

var (
	errUnauthenticated  = errors.New("handler: missing workspace id in request context")
	errPermissionDenied = errors.New("handler: role does not grant capture:write")
)
