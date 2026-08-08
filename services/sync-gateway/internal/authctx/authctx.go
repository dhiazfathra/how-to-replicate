// Package authctx carries the caller's authenticated identity
// (workspace/user/role) through a request context. Every RPC needs a
// caller-scoped workspace ID to enforce workspace isolation (see
// internal/gateway).
//
// OIDCMiddleware (Task 10) verifies a real OIDC bearer token and populates
// this context. HeaderMiddleware remains as a local-dev/test seam only —
// it trusts client-supplied headers and must never run against a
// deployment reachable by untrusted callers.
package authctx

import (
	"context"
	"net/http"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/dhiazfathra/how-to-replicate/services/internal/auth"
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

// MembershipResolver looks up the caller's role in a workspace. Callers
// (capture-api's membership store, in production) return ok=false for both
// "no such workspace" and "subject is not a member" — the two cases must
// be indistinguishable to the caller, per the RBAC non-disclosure
// requirement (see internal/authz).
type MembershipResolver interface {
	Resolve(ctx context.Context, subject, workspaceID string) (role string, ok bool, err error)
}

// OIDCMiddleware replaces HeaderMiddleware with real identity: it verifies
// the bearer token against the IdP via auth.Middleware, then resolves the
// verified subject's role in the workspace named by WorkspaceHeader through
// resolver, and injects subject/workspace/role into this package's context
// keys — so everything downstream of WorkspaceID(ctx)/UserID/Role, written
// against this seam since Task 4, is unchanged by Task 10 landing.
//
// The workspace header still names which workspace the caller intends to
// act in (a subject can belong to more than one) — it is no longer trusted
// for the role, which now comes from a real membership lookup. A request
// naming a workspace the subject isn't a member of gets the same 404 as a
// workspace that doesn't exist.
func OIDCMiddleware(verifier *oidc.IDTokenVerifier, resolver MembershipResolver) func(http.Handler) http.Handler {
	verify := auth.Middleware(verifier)
	return func(next http.Handler) http.Handler {
		adapter := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// auth.Middleware (verify, below) only ever calls next after
			// setting the subject via WithSubject — see its doc comment
			// ("next is never called" otherwise) — so this is never the
			// zero-value subject a failed Subject lookup would return.
			subject, _ := auth.Subject(r.Context())

			workspaceID := r.Header.Get(WorkspaceHeader)
			role, ok, err := resolver.Resolve(r.Context(), subject, workspaceID)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if !ok {
				http.NotFound(w, r)
				return
			}

			ctx := WithUserID(r.Context(), subject)
			ctx = WithWorkspaceID(ctx, workspaceID)
			ctx = WithRole(ctx, role)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
		return verify(adapter)
	}
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
