package authz

import "testing"

func TestCan(t *testing.T) {
	cases := []struct {
		role       Role
		permission Permission
		want       bool
	}{
		{RoleOwner, PermissionBillingManage, true},
		{RoleAdmin, PermissionBillingManage, false},
		{RoleAdmin, PermissionMemberRemove, true},
		{RoleMember, PermissionCaptureWrite, true},
		{RoleMember, PermissionCaptureDelete, false},
		{RoleViewer, PermissionCaptureRead, true},
		{RoleViewer, PermissionCaptureWrite, false},
		{Role("unknown"), PermissionCaptureRead, false},
		{RoleOwner, PermissionWorkspaceManage, true},
		{RoleAdmin, PermissionWorkspaceManage, true},
		{RoleMember, PermissionWorkspaceManage, false},
		{RoleViewer, PermissionWorkspaceManage, false},
		{RoleOwner, PermissionProjectManage, true},
		{RoleAdmin, PermissionProjectManage, true},
		{RoleMember, PermissionProjectManage, false},
		{RoleViewer, PermissionProjectManage, false},
	}

	for _, tc := range cases {
		if got := Can(tc.role, tc.permission); got != tc.want {
			t.Errorf("Can(%q, %q) = %v, want %v", tc.role, tc.permission, got, tc.want)
		}
	}
}

func TestPermissions(t *testing.T) {
	cases := []struct {
		role Role
		want int
	}{
		{RoleOwner, 8},
		{RoleAdmin, 7},
		{RoleMember, 2},
		{RoleViewer, 1},
		{Role("unknown"), 0},
	}

	for _, tc := range cases {
		got := Permissions(tc.role)
		if len(got) != tc.want {
			t.Errorf("Permissions(%q) has %d entries, want %d", tc.role, len(got), tc.want)
		}
	}
}

func TestIsValidRole(t *testing.T) {
	for _, r := range []Role{RoleOwner, RoleAdmin, RoleMember, RoleViewer} {
		if !IsValidRole(r) {
			t.Errorf("expected %q to be valid", r)
		}
	}
	if IsValidRole(Role("unknown")) {
		t.Error("expected unknown role to be invalid")
	}
}
