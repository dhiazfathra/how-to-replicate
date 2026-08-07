// Package authz implements workspace-scoped RBAC primitives: a role model
// and permission checks. Pure functions only — no I/O, no database lookups.
// Callers are responsible for loading the caller's role from wherever it
// lives (session, token claims, etc.) and passing it in.
package authz

// Permission is an action a role may or may not be allowed to perform.
type Permission string

// The closed set of permissions known to this package.
const (
	PermissionCaptureRead   Permission = "capture:read"
	PermissionCaptureWrite  Permission = "capture:write"
	PermissionCaptureDelete Permission = "capture:delete"
	PermissionMemberInvite  Permission = "member:invite"
	PermissionMemberRemove  Permission = "member:remove"
	PermissionBillingManage Permission = "billing:manage"
)

// Role is a workspace-scoped role name.
type Role string

// The closed set of workspace-scoped roles known to this package.
const (
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
	RoleViewer Role = "viewer"
)

// rolePermissions is the closed set of permissions each role grants. Roles
// not present here grant no permissions.
var rolePermissions = map[Role]map[Permission]struct{}{
	RoleOwner: set(
		PermissionCaptureRead, PermissionCaptureWrite, PermissionCaptureDelete,
		PermissionMemberInvite, PermissionMemberRemove, PermissionBillingManage,
	),
	RoleAdmin: set(
		PermissionCaptureRead, PermissionCaptureWrite, PermissionCaptureDelete,
		PermissionMemberInvite, PermissionMemberRemove,
	),
	RoleMember: set(
		PermissionCaptureRead, PermissionCaptureWrite,
	),
	RoleViewer: set(
		PermissionCaptureRead,
	),
}

func set(perms ...Permission) map[Permission]struct{} {
	m := make(map[Permission]struct{}, len(perms))
	for _, p := range perms {
		m[p] = struct{}{}
	}
	return m
}

// Can reports whether role grants permission. An unknown role grants
// nothing.
func Can(role Role, permission Permission) bool {
	perms, ok := rolePermissions[role]
	if !ok {
		return false
	}
	_, granted := perms[permission]
	return granted
}

// Permissions returns the sorted-by-declaration-order permission set for
// role, or nil for an unknown role.
func Permissions(role Role) []Permission {
	switch role {
	case RoleOwner:
		return []Permission{
			PermissionCaptureRead, PermissionCaptureWrite, PermissionCaptureDelete,
			PermissionMemberInvite, PermissionMemberRemove, PermissionBillingManage,
		}
	case RoleAdmin:
		return []Permission{
			PermissionCaptureRead, PermissionCaptureWrite, PermissionCaptureDelete,
			PermissionMemberInvite, PermissionMemberRemove,
		}
	case RoleMember:
		return []Permission{PermissionCaptureRead, PermissionCaptureWrite}
	case RoleViewer:
		return []Permission{PermissionCaptureRead}
	default:
		return nil
	}
}

// IsValidRole reports whether role is one of the known roles.
func IsValidRole(role Role) bool {
	_, ok := rolePermissions[role]
	return ok
}
