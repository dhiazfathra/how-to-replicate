package realtime

import (
	"testing"
	"time"

	"github.com/dhiazfathra/how-to-replicate/services/internal/authz"
)

func TestMembershipStore_UnknownEntryIsUnauthorized(t *testing.T) {
	m := NewMembershipStore()
	if _, ok := m.Check("ws1", "user1", time.Now()); ok {
		t.Fatalf("want unauthorized for an entry that was never seeded")
	}
}

func TestMembershipStore_EnsureSeededDoesNotOverwrite(t *testing.T) {
	m := NewMembershipStore()
	m.Set("ws1", "user1", Membership{Role: authz.RoleAdmin})
	m.EnsureSeeded("ws1", "user1", authz.RoleViewer)

	role, ok := m.Check("ws1", "user1", time.Now())
	if !ok || role != authz.RoleAdmin {
		t.Fatalf("want EnsureSeeded to leave the pre-existing Admin role alone, got role=%q ok=%v", role, ok)
	}
}

func TestMembershipStore_EnsureSeededFillsMissingEntry(t *testing.T) {
	m := NewMembershipStore()
	m.EnsureSeeded("ws1", "user1", authz.RoleViewer)

	role, ok := m.Check("ws1", "user1", time.Now())
	if !ok || role != authz.RoleViewer {
		t.Fatalf("want seeded Viewer role, got role=%q ok=%v", role, ok)
	}
}

func TestMembershipStore_Remove(t *testing.T) {
	m := NewMembershipStore()
	m.Set("ws1", "user1", Membership{Role: authz.RoleMember})
	m.Remove("ws1", "user1")

	if _, ok := m.Check("ws1", "user1", time.Now()); ok {
		t.Fatalf("want unauthorized after Remove")
	}
}

func TestMembershipStore_ExpiryBoundary(t *testing.T) {
	m := NewMembershipStore()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	m.Set("ws1", "user1", Membership{Role: authz.RoleMember, ExpiresAt: now})

	if _, ok := m.Check("ws1", "user1", now); ok {
		t.Fatalf("want expiry to take effect exactly at ExpiresAt (not after)")
	}
	if _, ok := m.Check("ws1", "user1", now.Add(-time.Nanosecond)); !ok {
		t.Fatalf("want authorized one nanosecond before expiry")
	}
}

func TestMembershipStore_NeverExpiresWhenZero(t *testing.T) {
	m := NewMembershipStore()
	m.Set("ws1", "user1", Membership{Role: authz.RoleMember})

	if _, ok := m.Check("ws1", "user1", time.Now().Add(1000*time.Hour)); !ok {
		t.Fatalf("want a zero ExpiresAt to never expire")
	}
}

func TestMembershipStore_RevokeWorkspaceOverridesIndividualMembership(t *testing.T) {
	m := NewMembershipStore()
	m.Set("ws1", "user1", Membership{Role: authz.RoleOwner})
	m.RevokeWorkspace("ws1")

	if _, ok := m.Check("ws1", "user1", time.Now()); ok {
		t.Fatalf("want workspace revocation to override an otherwise-valid membership")
	}
}

func TestMembershipStore_RevokeWorkspaceIsIsolatedPerWorkspace(t *testing.T) {
	m := NewMembershipStore()
	m.Set("ws1", "user1", Membership{Role: authz.RoleOwner})
	m.Set("ws2", "user1", Membership{Role: authz.RoleOwner})
	m.RevokeWorkspace("ws1")

	if _, ok := m.Check("ws2", "user1", time.Now()); !ok {
		t.Fatalf("want ws2's membership unaffected by ws1's revocation")
	}
}
