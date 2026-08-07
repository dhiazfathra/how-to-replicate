// Package realtime implements sync-gateway's WebSocket delta fan-out (Task
// 6). It is deliberately a thin, in-process layer over
// internal/gateway.Gateway.PullDeltas: a Hub wakes a connected socket when a
// new revision lands, and the socket loop then calls PullDeltas itself
// rather than duplicating its decode logic. This makes the socket exactly
// what the brief calls for — "a latency optimization over the pull, never
// the only path" — because the fast path and the recovery path are the same
// code.
//
// This is single-process fan-out only: Hub state lives in memory and is not
// shared across sync-gateway replicas. The brief scopes Task 6 to
// in-process/simple; multi-instance fan-out (e.g. Postgres LISTEN/NOTIFY, or
// a message bus) is a future task if sync-gateway is ever horizontally
// scaled.
package realtime

import "sync"

// Hub is an in-memory, workspace-scoped publish/subscribe point. Gateway
// calls Notify after committing a new mutation; connected sockets Subscribe
// to their workspace and wake on the next Notify for it.
type Hub struct {
	mu   sync.Mutex
	subs map[string]map[chan struct{}]struct{} // workspaceID -> set of notify channels
}

// NewHub returns an empty Hub.
func NewHub() *Hub {
	return &Hub{subs: map[string]map[chan struct{}]struct{}{}}
}

// Subscribe registers a new notify channel for workspaceID. The returned
// channel receives an empty struct (never closed) each time Notify(workspaceID)
// is called for as long as the subscription is live; call cancel when the
// caller is done (e.g. socket closed) to release it.
//
// The channel has capacity 1 and Notify sends non-blockingly: a pending
// notification is coalesced rather than queued, so an idle or slow
// subscriber never grows the Hub's memory — Notify never blocks on a
// subscriber, and a subscriber that never reads holds exactly one buffered
// slot, not an unbounded queue. Backpressure on the actual delta payload
// (not just the wake-up signal) is enforced by the socket loop itself: see
// internal/handler's WebSocket handler.
func (h *Hub) Subscribe(workspaceID string) (ch chan struct{}, cancel func()) {
	ch = make(chan struct{}, 1)
	h.mu.Lock()
	if h.subs[workspaceID] == nil {
		h.subs[workspaceID] = map[chan struct{}]struct{}{}
	}
	h.subs[workspaceID][ch] = struct{}{}
	h.mu.Unlock()

	cancel = func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.subs[workspaceID], ch)
		if len(h.subs[workspaceID]) == 0 {
			delete(h.subs, workspaceID)
		}
	}
	return ch, cancel
}

// Notify wakes every current subscriber of workspaceID. It never blocks and
// never touches any other workspace's subscribers — the workspace isolation
// the brief requires of the fan-out.
func (h *Hub) Notify(workspaceID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[workspaceID] {
		select {
		case ch <- struct{}{}:
		default:
			// Already has a pending wake-up; coalesce.
		}
	}
}
