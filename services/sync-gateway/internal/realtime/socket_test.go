package realtime

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"google.golang.org/protobuf/encoding/protojson"

	syncv1 "github.com/dhiazfathra/how-to-replicate/proto/gen/go/sync/v1"
	"github.com/dhiazfathra/how-to-replicate/services/internal/authz"
	"github.com/dhiazfathra/how-to-replicate/services/sync-gateway/internal/authctx"
)

// fakeGateway is a minimal, workspace-keyed DeltaPuller — no database, no
// gateway.Store — good enough for socket_test.go because the socket
// handler itself is what's under test here, not PullDeltas' own logic
// (that's covered in internal/gateway/deltas_test.go). It stores mutations
// per workspace in append order; index N is revision N+1, matching the real
// PullDeltas' "since is an exclusive revision cursor" contract.
type fakeGateway struct {
	mu   sync.Mutex
	data map[string][]*syncv1.Mutation
}

func newFakeGateway() *fakeGateway {
	return &fakeGateway{data: map[string][]*syncv1.Mutation{}}
}

func (g *fakeGateway) push(workspaceID string, m *syncv1.Mutation) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.data[workspaceID] = append(g.data[workspaceID], m)
}

func (g *fakeGateway) PullDeltas(_ context.Context, workspaceID string, since int64, limit int32) ([]*syncv1.Mutation, int64, bool, []*syncv1.CaptureSyncState, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	all := g.data[workspaceID]
	if since < 0 || since > int64(len(all)) {
		since = int64(len(all))
	}
	rest := all[since:]
	if limit <= 0 {
		limit = 500
	}
	hasMore := int32(len(rest)) > limit //nolint:gosec // test fixture, len(rest) bounded by test data size
	if hasMore {
		rest = rest[:limit]
	}
	return rest, since + int64(len(rest)), hasMore, nil, nil
}

func setTitleMutation(id, title string) *syncv1.Mutation {
	return &syncv1.Mutation{
		Id:        id,
		CaptureId: "cap_1",
		Op:        &syncv1.Mutation_SetTitle{SetTitle: &syncv1.SetTitle{Title: title}},
	}
}

func newTestServer(t *testing.T, h *Handler) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("/ws", authctx.HeaderMiddleware(h))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
}

func dial(t *testing.T, wsURL, workspaceID, userID, role string) *websocket.Conn {
	t.Helper()
	hdr := http.Header{}
	hdr.Set(authctx.WorkspaceHeader, workspaceID)
	hdr.Set(authctx.UserHeader, userID)
	hdr.Set(authctx.RoleHeader, role)
	conn, _, err := websocket.Dial(context.Background(), wsURL, &websocket.DialOptions{HTTPHeader: hdr}) //nolint:bodyclose // coder/websocket docs: "You never need to close resp.Body yourself."
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return conn
}

func readBatch(t *testing.T, conn *websocket.Conn) (deltaBatch, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var b deltaBatch
	err := wsjson.Read(ctx, conn, &b)
	return b, err
}

func decodeBatchIDs(t *testing.T, b deltaBatch) []string {
	t.Helper()
	ids := make([]string, 0, len(b.Mutations))
	for _, raw := range b.Mutations {
		var m syncv1.Mutation
		if err := protojson.Unmarshal(raw, &m); err != nil {
			t.Fatalf("unmarshal mutation: %v", err)
		}
		ids = append(ids, m.GetId())
	}
	return ids
}

func TestSocket_DeliversNewDeltaAfterNotify(t *testing.T) {
	gw := newFakeGateway()
	hub := NewHub()
	h := &Handler{Gateway: gw, Hub: hub, Membership: NewMembershipStore(), WriteTimeout: time.Second}
	wsURL := newTestServer(t, h)

	conn := dial(t, wsURL, "ws1", "user1", string(authz.RoleMember))
	defer func() { _ = conn.CloseNow() }()

	gw.push("ws1", setTitleMutation("m1", "hello"))
	hub.Notify("ws1")

	b, err := readBatch(t, conn)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	ids := decodeBatchIDs(t, b)
	if len(ids) != 1 || ids[0] != "m1" {
		t.Fatalf("want [m1], got %+v", ids)
	}
	if b.Revision != 1 {
		t.Fatalf("want revision 1, got %d", b.Revision)
	}
}

func TestSocket_CrossWorkspaceIsolation(t *testing.T) {
	gw := newFakeGateway()
	hub := NewHub()
	h := &Handler{Gateway: gw, Hub: hub, Membership: NewMembershipStore(), WriteTimeout: time.Second}
	wsURL := newTestServer(t, h)

	connB := dial(t, wsURL, "wsB", "userB", string(authz.RoleMember))
	defer func() { _ = connB.CloseNow() }()

	gw.push("wsA", setTitleMutation("a1", "hello"))
	hub.Notify("wsA")

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	var b deltaBatch
	if err := wsjson.Read(ctx, connB, &b); err == nil {
		t.Fatalf("want no message delivered to an unrelated workspace's socket, got %+v", b)
	}
}

func TestSocket_ReconnectAfterGapConvergesViaPull(t *testing.T) {
	gw := newFakeGateway()
	hub := NewHub()
	h := &Handler{Gateway: gw, Hub: hub, Membership: NewMembershipStore(), WriteTimeout: time.Second}
	wsURL := newTestServer(t, h)

	// Client A stays connected the whole time and accumulates every delta
	// live, over the socket.
	connA := dial(t, wsURL, "ws1", "userA", string(authz.RoleMember))
	defer func() { _ = connA.CloseNow() }()

	seenLive := map[string]bool{}

	gw.push("ws1", setTitleMutation("m1", "a"))
	hub.Notify("ws1")
	b, err := readBatch(t, connA)
	if err != nil {
		t.Fatalf("read 1: %v", err)
	}
	for _, id := range decodeBatchIDs(t, b) {
		seenLive[id] = true
	}

	// Three more revisions land while a second client is disconnected
	// entirely — simulating "missed N revisions while offline".
	gw.push("ws1", setTitleMutation("m2", "b"))
	gw.push("ws1", setTitleMutation("m3", "c"))
	gw.push("ws1", setTitleMutation("m4", "d"))
	hub.Notify("ws1")
	b, err = readBatch(t, connA)
	if err != nil {
		t.Fatalf("read 2: %v", err)
	}
	for _, id := range decodeBatchIDs(t, b) {
		seenLive[id] = true
	}

	// The disconnected client "reconnects" by calling the exact same
	// PullDeltas method the socket loop itself calls (since=0, i.e. from
	// scratch) — this is the recovery path the brief requires, and it must
	// converge on the identical set the live socket saw.
	recovered, _, _, _, err := gw.PullDeltas(context.Background(), "ws1", 0, 0)
	if err != nil {
		t.Fatalf("PullDeltas: %v", err)
	}
	seenRecovered := map[string]bool{}
	for _, m := range recovered {
		seenRecovered[m.GetId()] = true
	}

	if len(seenLive) != 4 || len(seenRecovered) != 4 {
		t.Fatalf("want 4 mutations each, live=%d recovered=%d", len(seenLive), len(seenRecovered))
	}
	for id := range seenLive {
		if !seenRecovered[id] {
			t.Fatalf("reconnect-via-pull missed %s that the live socket saw", id)
		}
	}
}

func TestSocket_SlowConsumerDisconnected(t *testing.T) {
	gw := newFakeGateway()
	hub := NewHub()
	h := &Handler{
		Gateway:      gw,
		Hub:          hub,
		Membership:   NewMembershipStore(),
		WriteTimeout: 10 * time.Millisecond,
		// Deterministically simulate a write that never finishes before the
		// deadline, instead of racing the OS's TCP send buffer (real, but
		// not a reliable thing to depend on in a unit test).
		WriteJSON: func(ctx context.Context, _ *websocket.Conn, _ any) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}
	wsURL := newTestServer(t, h)

	conn := dial(t, wsURL, "ws1", "user1", string(authz.RoleMember))
	defer func() { _ = conn.CloseNow() }()

	gw.push("ws1", setTitleMutation("m1", "a"))
	hub.Notify("ws1")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var b deltaBatch
	err := wsjson.Read(ctx, conn, &b)
	if err == nil {
		t.Fatalf("want the slow consumer disconnected, got batch %+v", b)
	}
	var ce websocket.CloseError
	if !errors.As(err, &ce) || ce.Code != websocket.StatusPolicyViolation {
		t.Fatalf("want StatusPolicyViolation close, got %v", err)
	}
}

// testCloseOnReauthFailure drives the shared shape of all four
// revalidation scenarios: connect, receive one delta to prove the socket is
// live, mutate the membership store the way the scenario names, push
// another delta, and assert the connection closes before that delta
// arrives.
func testCloseOnReauthFailure(t *testing.T, mutate func(m *MembershipStore, workspaceID, userID string)) {
	t.Helper()
	gw := newFakeGateway()
	hub := NewHub()
	membership := NewMembershipStore()
	h := &Handler{Gateway: gw, Hub: hub, Membership: membership, WriteTimeout: time.Second}
	wsURL := newTestServer(t, h)

	conn := dial(t, wsURL, "ws1", "user1", string(authz.RoleMember))
	defer func() { _ = conn.CloseNow() }()

	gw.push("ws1", setTitleMutation("m1", "a"))
	hub.Notify("ws1")
	if _, err := readBatch(t, conn); err != nil {
		t.Fatalf("initial read: %v", err)
	}

	mutate(membership, "ws1", "user1")

	gw.push("ws1", setTitleMutation("m2", "b"))
	hub.Notify("ws1")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var b deltaBatch
	err := wsjson.Read(ctx, conn, &b)
	if err == nil {
		t.Fatalf("want socket closed after revalidation failure, got batch %+v", b)
	}
	var ce websocket.CloseError
	if !errors.As(err, &ce) || ce.Code != websocket.StatusPolicyViolation {
		t.Fatalf("want StatusPolicyViolation close, got %v", err)
	}
}

func TestSocket_MembershipRemovalClosesSocket(t *testing.T) {
	testCloseOnReauthFailure(t, func(m *MembershipStore, workspaceID, userID string) {
		m.Remove(workspaceID, userID)
	})
}

func TestSocket_RoleDowngradeClosesSocket(t *testing.T) {
	testCloseOnReauthFailure(t, func(m *MembershipStore, workspaceID, userID string) {
		// An unknown role grants nothing (authz.Can), simulating a
		// downgrade to a role with no capture:read permission.
		m.Set(workspaceID, userID, Membership{Role: authz.Role("suspended")})
	})
}

func TestSocket_TokenExpiryClosesSocket(t *testing.T) {
	testCloseOnReauthFailure(t, func(m *MembershipStore, workspaceID, userID string) {
		m.Set(workspaceID, userID, Membership{Role: authz.RoleMember, ExpiresAt: time.Now().Add(-time.Second)})
	})
}

func TestSocket_WorkspaceRevocationClosesSocket(t *testing.T) {
	testCloseOnReauthFailure(t, func(m *MembershipStore, workspaceID, _ string) {
		m.RevokeWorkspace(workspaceID)
	})
}

func TestSocket_UnauthenticatedRejectedAtHandshake(t *testing.T) {
	gw := newFakeGateway()
	h := &Handler{Gateway: gw, Hub: NewHub(), Membership: NewMembershipStore()}
	mux := http.NewServeMux()
	mux.Handle("/ws", authctx.HeaderMiddleware(h))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/ws", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
}

// TestSocket_NonWebSocketRequestRejected drives an authorized request that
// isn't a WebSocket upgrade at all (no Connection/Upgrade headers) — proving
// websocket.Accept's error is handled (returns early) instead of panicking
// on a nil *websocket.Conn.
func TestSocket_NonWebSocketRequestRejected(t *testing.T) {
	gw := newFakeGateway()
	h := &Handler{Gateway: gw, Hub: NewHub(), Membership: NewMembershipStore()}
	mux := http.NewServeMux()
	mux.Handle("/ws", authctx.HeaderMiddleware(h))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/ws", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set(authctx.WorkspaceHeader, "ws1")
	req.Header.Set(authctx.UserHeader, "user1")
	req.Header.Set(authctx.RoleHeader, string(authz.RoleMember))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusSwitchingProtocols {
		t.Fatalf("want the handshake to fail for a non-upgrade request, got 101")
	}
}

func TestSocket_InsufficientRoleRejectedAtHandshake(t *testing.T) {
	gw := newFakeGateway()
	h := &Handler{Gateway: gw, Hub: NewHub(), Membership: NewMembershipStore()}
	mux := http.NewServeMux()
	mux.Handle("/ws", authctx.HeaderMiddleware(h))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/ws", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set(authctx.WorkspaceHeader, "ws1")
	req.Header.Set(authctx.UserHeader, "user1")
	// No role header at all -> unknown role -> grants nothing.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
}

func TestSocket_PullDeltasErrorClosesSocket(t *testing.T) {
	hub := NewHub()
	h := &Handler{
		Gateway:      erroringGateway{},
		Hub:          hub,
		Membership:   NewMembershipStore(),
		WriteTimeout: time.Second,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	wsURL := newTestServer(t, h)

	conn := dial(t, wsURL, "ws1", "user1", string(authz.RoleMember))
	defer func() { _ = conn.CloseNow() }()

	hub.Notify("ws1")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var b deltaBatch
	err := wsjson.Read(ctx, conn, &b)
	if err == nil {
		t.Fatalf("want socket closed after PullDeltas error, got batch %+v", b)
	}
}

// TestSocket_DefaultsApply exercises Handler with every optional field left
// zero-valued (Now, WriteTimeout, WriteJSON, Logger, PageSize), proving the
// documented defaults (time.Now, 5s, wsjson.Write, slog.Default, 500) are
// what actually run, not just what the doc comments claim.
func TestSocket_DefaultsApply(t *testing.T) {
	gw := newFakeGateway()
	hub := NewHub()
	h := &Handler{Gateway: gw, Hub: hub, Membership: NewMembershipStore()}
	wsURL := newTestServer(t, h)

	conn := dial(t, wsURL, "ws1", "user1", string(authz.RoleMember))
	defer func() { _ = conn.CloseNow() }()

	gw.push("ws1", setTitleMutation("m1", "hello"))
	hub.Notify("ws1")

	b, err := readBatch(t, conn)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if ids := decodeBatchIDs(t, b); len(ids) != 1 || ids[0] != "m1" {
		t.Fatalf("want [m1], got %+v", ids)
	}
}

// TestSocket_CustomNowDrivesExpiry proves the injected Now (not time.Now)
// is what the reauth check actually uses: a membership expiring "now" by
// the injected clock closes the socket even though the real wall clock
// hasn't reached it.
func TestSocket_CustomNowDrivesExpiry(t *testing.T) {
	gw := newFakeGateway()
	hub := NewHub()
	membership := NewMembershipStore()
	fixedNow := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	membership.Set("ws1", "user1", Membership{Role: authz.RoleMember, ExpiresAt: fixedNow})

	h := &Handler{
		Gateway:      gw,
		Hub:          hub,
		Membership:   membership,
		WriteTimeout: time.Second,
		Now:          func() time.Time { return fixedNow },
	}
	wsURL := newTestServer(t, h)

	conn := dial(t, wsURL, "ws1", "user1", string(authz.RoleMember))
	defer func() { _ = conn.CloseNow() }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var b deltaBatch
	err := wsjson.Read(ctx, conn, &b)
	if err == nil {
		t.Fatalf("want socket closed at the injected expiry, got batch %+v", b)
	}
}

// TestWaitForNext covers both branches of the socket loop's wait step
// directly and deterministically — driving it through a real client
// disconnect would depend on how fast the OS/http.Server notices the TCP
// close, which is not a reliable thing to assert a test on.
func TestWaitForNext(t *testing.T) {
	t.Run("ctx done stops the loop", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if waitForNext(ctx, make(chan struct{})) {
			t.Fatalf("want false once ctx is done")
		}
	})

	t.Run("notify continues the loop", func(t *testing.T) {
		notify := make(chan struct{}, 1)
		notify <- struct{}{}
		if !waitForNext(context.Background(), notify) {
			t.Fatalf("want true on notify")
		}
	})
}

// TestSocket_PullDeltasErrorUsesDefaultLogger hits the same error path as
// TestSocket_PullDeltasErrorClosesSocket but with Logger left unset, so
// logger()'s slog.Default() branch runs too, not just the injected one.
func TestSocket_PullDeltasErrorUsesDefaultLogger(t *testing.T) {
	hub := NewHub()
	h := &Handler{Gateway: erroringGateway{}, Hub: hub, Membership: NewMembershipStore(), WriteTimeout: time.Second}
	wsURL := newTestServer(t, h)

	conn := dial(t, wsURL, "ws1", "user1", string(authz.RoleMember))
	defer func() { _ = conn.CloseNow() }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var b deltaBatch
	if err := wsjson.Read(ctx, conn, &b); err == nil {
		t.Fatalf("want socket closed after PullDeltas error, got batch %+v", b)
	}
}

// TestSocket_EncodeBatchErrorClosesSocket forces protojson.Marshal to fail
// (a string field carrying invalid UTF-8, which the closed mutation
// vocabulary should never actually produce, but proto enforces at encode
// time regardless of how it got there) to prove encodeBatch's error branch
// closes the socket instead of writing a corrupt/partial frame.
func TestSocket_EncodeBatchErrorClosesSocket(t *testing.T) {
	gw := newFakeGateway()
	gw.push("ws1", &syncv1.Mutation{
		Id: "bad",
		Op: &syncv1.Mutation_SetTitle{SetTitle: &syncv1.SetTitle{Title: "\xff\xfe not utf-8"}},
	})
	hub := NewHub()
	h := &Handler{Gateway: gw, Hub: hub, Membership: NewMembershipStore(), WriteTimeout: time.Second}
	wsURL := newTestServer(t, h)

	conn := dial(t, wsURL, "ws1", "user1", string(authz.RoleMember))
	defer func() { _ = conn.CloseNow() }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var b deltaBatch
	if err := wsjson.Read(ctx, conn, &b); err == nil {
		t.Fatalf("want socket closed after an encode failure, got batch %+v", b)
	}
}

// TestSocket_RequestContextEndingClosesSocket drives the ctx.Done() branch
// of the socket loop's wait step end to end: a request-scoped deadline
// (standing in for the server shutting down, or any other reason the
// request context ends) fires while the socket is idle waiting on notify,
// and the handler must stop and close the connection rather than leak a
// goroutine parked there forever.
func TestSocket_RequestContextEndingClosesSocket(t *testing.T) {
	gw := newFakeGateway()
	h := &Handler{Gateway: gw, Hub: NewHub(), Membership: NewMembershipStore(), WriteTimeout: time.Second}

	mux := http.NewServeMux()
	deadlined := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), 100*time.Millisecond)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	mux.Handle("/ws", deadlined(authctx.HeaderMiddleware(h)))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

	conn := dial(t, wsURL, "ws1", "user1", string(authz.RoleMember))
	defer func() { _ = conn.CloseNow() }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var b deltaBatch
	if err := wsjson.Read(ctx, conn, &b); err == nil {
		t.Fatalf("want the socket closed once its request context ends, got batch %+v", b)
	}
}

type erroringGateway struct{}

func (erroringGateway) PullDeltas(context.Context, string, int64, int32) ([]*syncv1.Mutation, int64, bool, []*syncv1.CaptureSyncState, error) {
	return nil, 0, false, nil, errors.New("boom")
}
