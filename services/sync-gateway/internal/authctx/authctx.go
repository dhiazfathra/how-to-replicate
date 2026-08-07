// Package authctx is the auth seam sync-gateway builds against until Task
// 10 lands real identity (OIDC/JWT verification, workspaces, RBAC). Every
// RPC needs a caller-scoped workspace ID to enforce workspace isolation
// (see internal/gateway), but nothing yet issues or verifies a real
// credential that carries one.
//
// TODO(Task 10): replace HeaderMiddleware with real auth middleware that
// verifies a JWT/session and injects the workspace ID it authenticates,
// instead of trusting a client-supplied header. Everything downstream of
// WorkspaceID(ctx) is already written against this seam and does not
// change when that happens.
package authctx

import (
	"context"
	"net/http"
)

type contextKey int

const (
	workspaceIDKey contextKey = iota
	userIDKey
	roleKey
)

// WorkspaceHeader, UserHeader, and RoleHeader are the headers
// HeaderMiddleware reads. Trusting client-supplied headers for identity is
// only acceptable because nothing else exists yet — see the package TODO.
const (
	WorkspaceHeader = "X-Htr-Workspace-Id"
	UserHeader      = "X-Htr-User-Id"
	RoleHeader      = "X-Htr-Role"
)

// WithWorkspaceID returns a copy of ctx carrying workspaceID.
func WithWorkspaceID(ctx context.Context, workspaceID string) context.Context {
	return context.WithValue(ctx, workspaceIDKey, workspaceID)
}

// WorkspaceID returns the workspace ID carried by ctx, and whether one was
// present.
func WorkspaceID(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(workspaceIDKey).(string)
	if !ok || v == "" {
		return "", false
	}
	return v, true
}

// WithUserID returns a copy of ctx carrying userID.
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDKey, userID)
}

// UserID returns the user ID carried by ctx, and whether one was present.
func UserID(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(userIDKey).(string)
	if !ok || v == "" {
		return "", false
	}
	return v, true
}

// WithRole returns a copy of ctx carrying the caller's workspace-scoped
// role (see internal/authz.Role).
func WithRole(ctx context.Context, role string) context.Context {
	return context.WithValue(ctx, roleKey, role)
}

// Role returns the role carried by ctx, and whether one was present.
func Role(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(roleKey).(string)
	if !ok || v == "" {
		return "", false
	}
	return v, true
}

// HeaderMiddleware injects the caller's workspace ID, user ID, and role
// from WorkspaceHeader/UserHeader/RoleHeader into the request context.
// This is the placeholder seam described in the package doc: it makes the
// workspace-scoping and permission checks downstream real and testable
// today, without building a fake JWT system to stand in for Task 10.
func HeaderMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := WithWorkspaceID(r.Context(), r.Header.Get(WorkspaceHeader))
		ctx = WithUserID(ctx, r.Header.Get(UserHeader))
		ctx = WithRole(ctx, r.Header.Get(RoleHeader))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
