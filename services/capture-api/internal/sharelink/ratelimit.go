package sharelink

import (
	"sync"
	"time"
)

// Limiter is an in-process fixed-window rate limiter keyed by an arbitrary
// string (the resolver keys it by client IP). One service instance per
// ADR-002/ADR-012's single-deployment posture, so in-process state is
// sufficient — no Redis needed to stop token brute-forcing.
//
// ponytail: fixed window, not sliding/token-bucket — allows up to 2x Max
// requests across a window boundary. Good enough to make brute-forcing a
// 128-bit HMAC infeasible; upgrade to a token bucket if that boundary burst
// ever matters.
type Limiter struct {
	Max    int
	Window time.Duration

	mu   sync.Mutex
	seen map[string]*window
}

type window struct {
	start time.Time
	count int
}

// Allow reports whether key may make another attempt right now, recording
// the attempt either way (a rejected attempt still counts, so retrying
// faster doesn't buy extra tries).
func (l *Limiter) Allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen == nil {
		l.seen = make(map[string]*window)
	}
	w, ok := l.seen[key]
	if !ok || now.Sub(w.start) >= l.Window {
		w = &window{start: now}
		l.seen[key] = w
	}
	w.count++
	return w.count <= l.Max
}
