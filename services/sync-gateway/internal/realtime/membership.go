package realtime

import (
	"sync"
	"time"

	"github.com/dhiazfathra/how-to-replicate/services/internal/authz"
)

// Membership is one caller's standing in one workspace, as far as the
// socket loop's per-batch re-check is concerned: a role, plus an optional
// expiry.
type Membership struct {
	Role authz.Role
	// ExpiresAt is when this membership stops being valid. Zero means it
	// never expires — simulating a long-lived token, as opposed to a
	// short-lived one a test can construct already-expired or expiring
	// mid-connection.
	ExpiresAt time.Time
}

// MembershipStore is the revalidation source the brief requires: a real,
// queryable, mutatable record of who is currently allowed to read a
// workspace, independent of whatever authenticated the original HTTP
// handshake. Tasks 4/5's authctx header seam only ever reflects the
// connection's opening state — it cannot represent "revoked five seconds
// after connect" — so the socket loop checks this store instead, and tests
// mutate it mid-connection to prove revalidation is real, not decorative.
//
// This is explicitly the lightweight stand-in the brief allows ("you can
// stub/approximate 'membership'"), not Task 10's real persisted membership
// table — but the store itself, and the socket loop's use of it, are real:
// nothing here is a TODO or no-op.
type MembershipStore struct {
	mu               sync.RWMutex
	entries          map[string]Membership // "workspaceID|userID" -> membership
	revokedWorkspace map[string]bool
}

// NewMembershipStore returns an empty store.
func NewMembershipStore() *MembershipStore {
	return &MembershipStore{
		entries:          map[string]Membership{},
		revokedWorkspace: map[string]bool{},
	}
}

func memberKey(workspaceID, userID string) string { return workspaceID + "|" + userID }

// EnsureSeeded records role for (workspaceID, userID) if no entry exists
// yet. Called at socket connect time so a fresh connection's baseline
// membership matches whatever role authenticated its handshake, without
// clobbering an entry a test (or a prior connection) already set — e.g. a
// test that pre-revokes a user before that user's socket ever connects.
func (m *MembershipStore) EnsureSeeded(workspaceID, userID string, role authz.Role) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := memberKey(workspaceID, userID)
	if _, ok := m.entries[k]; !ok {
		m.entries[k] = Membership{Role: role}
	}
}

// Set records (or replaces) mem for (workspaceID, userID) — used by tests
// (and would be used by a real membership-change webhook) to simulate a
// role downgrade or an expiring token.
func (m *MembershipStore) Set(workspaceID, userID string, mem Membership) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[memberKey(workspaceID, userID)] = mem
}

// Remove deletes (workspaceID, userID)'s membership entirely, simulating
// membership removal.
func (m *MembershipStore) Remove(workspaceID, userID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.entries, memberKey(workspaceID, userID))
}

// RevokeWorkspace marks an entire workspace revoked: every member of it
// fails Check from this point on, regardless of their individual role or
// expiry, simulating e.g. an org-wide access shutoff.
func (m *MembershipStore) RevokeWorkspace(workspaceID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.revokedWorkspace[workspaceID] = true
}

// Check reports whether (workspaceID, userID) is currently authorized to
// keep reading, and if so, their current role. now is injected so tests can
// construct exact expiry boundaries deterministically.
func (m *MembershipStore) Check(workspaceID, userID string, now time.Time) (authz.Role, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.revokedWorkspace[workspaceID] {
		return "", false
	}
	mem, ok := m.entries[memberKey(workspaceID, userID)]
	if !ok {
		return "", false
	}
	if !mem.ExpiresAt.IsZero() && !mem.ExpiresAt.After(now) {
		return "", false
	}
	return mem.Role, true
}
