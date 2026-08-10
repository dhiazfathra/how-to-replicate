package auth

import (
	"context"
	"sync"
)

// MemoryReplayGuard is a mutex-guarded in-memory ReplayGuard. It never
// evicts entries, which is fine for the web viewer's login flow (one token
// per human sign-in, process lifetime is short); a long-lived server would
// want a TTL, but that's speculative for this client.
//
// ponytail: unbounded map, add TTL eviction if this guard ever backs a
// long-running process with high sign-in volume.
type MemoryReplayGuard struct {
	mu   sync.Mutex
	seen map[string]struct{}
}

// NewMemoryReplayGuard returns an empty MemoryReplayGuard.
func NewMemoryReplayGuard() *MemoryReplayGuard {
	return &MemoryReplayGuard{seen: make(map[string]struct{})}
}

// SeenBefore reports whether tokenID was already marked seen.
func (g *MemoryReplayGuard) SeenBefore(_ context.Context, tokenID string) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.seen[tokenID]
	return ok, nil
}

// MarkSeen records tokenID as consumed.
func (g *MemoryReplayGuard) MarkSeen(_ context.Context, tokenID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.seen[tokenID] = struct{}{}
	return nil
}
