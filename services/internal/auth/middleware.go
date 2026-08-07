package auth

// This file verifies a Bearer token from the Authorization header against
// an OIDC verifier and injects the caller's subject and claims into the
// request context, mirroring the WithX/X(ctx) accessor style used by
// sync-gateway's internal/authctx.
//
// This middleware resolves identity only. It does NOT resolve workspace
// role or membership — sync-gateway and capture-api read Subject(ctx) and
// do their own `memberships` lookup downstream. That keeps the shared auth
// package ignorant of any one service's authorization model.

import (
	"context"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
)

type contextKey int

const (
	subjectKey contextKey = iota
	claimsKey
)

// WithSubject returns a copy of ctx carrying the verified token subject.
func WithSubject(ctx context.Context, subject string) context.Context {
	return context.WithValue(ctx, subjectKey, subject)
}

// Subject returns the subject carried by ctx, and whether one was present.
func Subject(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(subjectKey).(string)
	if !ok || v == "" {
		return "", false
	}
	return v, true
}

// WithClaims returns a copy of ctx carrying the verified token's full claim
// set.
func WithClaims(ctx context.Context, claims map[string]interface{}) context.Context {
	return context.WithValue(ctx, claimsKey, claims)
}

// Claims returns the claims carried by ctx, and whether any were present.
func Claims(ctx context.Context) (map[string]interface{}, bool) {
	v, ok := ctx.Value(claimsKey).(map[string]interface{})
	if !ok || v == nil {
		return nil, false
	}
	return v, true
}

// Middleware returns net/http middleware (chi routers accept this directly)
// that verifies the request's Bearer token with verifier, and on success
// injects the token's subject and claims into the request context before
// calling next. A missing header, a malformed header, or a token that
// fails verification (bad signature, wrong issuer/audience, expired) gets
// a 401 and next is never called.
func Middleware(verifier *oidc.IDTokenVerifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := bearerToken(r.Header.Get("Authorization"))
			if raw == "" {
				http.Error(w, "missing bearer token", http.StatusUnauthorized)
				return
			}

			idToken, err := verifier.Verify(r.Context(), raw)
			if err != nil {
				http.Error(w, "invalid token", http.StatusUnauthorized)
				return
			}

			var claims map[string]interface{}
			if err := idToken.Claims(&claims); err != nil || idToken.Subject == "" {
				http.Error(w, "invalid token claims", http.StatusUnauthorized)
				return
			}

			ctx := WithSubject(r.Context(), idToken.Subject)
			ctx = WithClaims(ctx, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func bearerToken(header string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, prefix))
}
