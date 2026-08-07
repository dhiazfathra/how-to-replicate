package realtime

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"google.golang.org/protobuf/encoding/protojson"

	syncv1 "github.com/dhiazfathra/how-to-replicate/proto/gen/go/sync/v1"
	"github.com/dhiazfathra/how-to-replicate/services/internal/authz"
	"github.com/dhiazfathra/how-to-replicate/services/sync-gateway/internal/authctx"
)

// DeltaPuller is the subset of gateway.Gateway the socket handler needs. It
// exists so tests can drive the handler against a fake without a database,
// and so this package never has to know about gateway.Store.
type DeltaPuller interface {
	PullDeltas(ctx context.Context, workspaceID string, since int64, limit int32) (mutations []*syncv1.Mutation, revision int64, hasMore bool, err error)
}

// deltaBatch is the wire shape sent over the socket: each mutation is
// pre-encoded protojson (so the oneof round-trips correctly, which
// encoding/json alone does not do for generated proto structs), wrapped
// with the batch's resulting revision cursor.
type deltaBatch struct {
	Mutations []json.RawMessage `json:"mutations"`
	Revision  int64             `json:"revision"`
}

// Handler serves the delta fan-out WebSocket endpoint. It requires the same
// authctx middleware as the ConnectRPC handlers (internal/handler) to be
// mounted in front of it, since it reads workspace/user/role from request
// context the same way.
type Handler struct {
	Gateway    DeltaPuller
	Hub        *Hub
	Membership *MembershipStore

	// Now returns the current time for expiry checks. Defaults to time.Now.
	Now func() time.Time

	// WriteTimeout bounds how long one delta-batch write may take. A
	// consumer that hasn't drained its TCP receive buffer within this
	// window is treated as a slow consumer and disconnected — never
	// buffered without bound. Defaults to 5s.
	WriteTimeout time.Duration

	// PageSize bounds how many mutations one PullDeltas call inside the
	// socket loop returns. Defaults to 500 (gateway's own default) via 0.
	PageSize int32

	// WriteJSON sends one batch over conn. Defaults to wsjson.Write. Tests
	// override it to make "the write didn't finish before the deadline"
	// deterministic instead of depending on the OS's TCP send buffer
	// filling up, which is real backpressure but not a reliable thing to
	// race against in a unit test.
	WriteJSON func(ctx context.Context, conn *websocket.Conn, v any) error

	Logger *slog.Logger
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now().UTC()
}

func (h *Handler) writeTimeout() time.Duration {
	if h.WriteTimeout > 0 {
		return h.WriteTimeout
	}
	return 5 * time.Second
}

func (h *Handler) writeJSON() func(context.Context, *websocket.Conn, any) error {
	if h.WriteJSON != nil {
		return h.WriteJSON
	}
	return wsjson.Write
}

func (h *Handler) logger() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}

// ServeHTTP upgrades the connection, then loops: re-authorize, pull
// whatever is new since the last batch sent, write it, and wait for either
// a Hub notification or the request context ending. Authorization is
// re-checked at the top of every iteration — including the first, before
// anything is ever sent — so a membership that was already invalid at
// connect time never gets a first delta either.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	workspaceID, ok := authctx.WorkspaceID(ctx)
	userID, _ := authctx.UserID(ctx)
	role, _ := authctx.Role(ctx)
	if !ok || !authz.Can(authz.Role(role), authz.PermissionCaptureRead) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()

	h.Membership.EnsureSeeded(workspaceID, userID, authz.Role(role))

	notify, cancel := h.Hub.Subscribe(workspaceID)
	defer cancel()

	var since int64
	for {
		currentRole, authorized := h.Membership.Check(workspaceID, userID, h.now())
		if !authorized {
			_ = conn.Close(websocket.StatusPolicyViolation, "authorization revoked")
			return
		}
		if !authz.Can(currentRole, authz.PermissionCaptureRead) {
			_ = conn.Close(websocket.StatusPolicyViolation, "insufficient role")
			return
		}

		mutations, revision, _, err := h.Gateway.PullDeltas(ctx, workspaceID, since, h.PageSize)
		if err != nil {
			h.logger().Error("realtime: pull deltas", "error", err, "workspace_id", workspaceID)
			_ = conn.Close(websocket.StatusInternalError, "pull failed")
			return
		}

		if len(mutations) > 0 {
			batch, err := encodeBatch(mutations, revision)
			if err != nil {
				h.logger().Error("realtime: encode batch", "error", err, "workspace_id", workspaceID)
				_ = conn.Close(websocket.StatusInternalError, "encode failed")
				return
			}
			writeCtx, writeCancel := context.WithTimeout(ctx, h.writeTimeout())
			err = h.writeJSON()(writeCtx, conn, batch)
			writeCancel()
			if err != nil {
				// Slow or gone consumer: disconnect rather than buffer.
				// This write IS the backpressure point — no unbounded queue
				// sits in front of it — so a write that doesn't finish
				// inside WriteTimeout means the peer isn't draining, and
				// the connection is dropped rather than piling up data
				// behind it.
				_ = conn.Close(websocket.StatusPolicyViolation, "slow consumer")
				return
			}
			since = revision
		}

		if !waitForNext(ctx, notify) {
			return
		}
	}
}

// waitForNext blocks until either notify fires (ok=true: loop again) or ctx
// ends (ok=false: the client disconnected or the server is shutting down,
// so the loop must stop rather than leak a goroutine parked on notify
// forever).
func waitForNext(ctx context.Context, notify <-chan struct{}) bool {
	select {
	case <-ctx.Done():
		return false
	case <-notify:
		return true
	}
}

// encodeBatch protojson-encodes each mutation individually (rather than the
// whole response as one proto message) because there is no proto message
// for "a batch of full Mutations plus a revision" that matches
// PullDeltasResponse's wire shape without also carrying has_more, which the
// socket has no use for — it always resumes at revision.
func encodeBatch(mutations []*syncv1.Mutation, revision int64) (deltaBatch, error) {
	out := deltaBatch{Mutations: make([]json.RawMessage, 0, len(mutations)), Revision: revision}
	for _, m := range mutations {
		b, err := protojson.Marshal(m)
		if err != nil {
			return deltaBatch{}, err
		}
		out.Mutations = append(out.Mutations, b)
	}
	return out, nil
}
