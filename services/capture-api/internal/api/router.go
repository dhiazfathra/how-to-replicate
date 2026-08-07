// Package api implements capture-api's HTTP surface: workspace and project
// CRUD, gated by workspace-scoped RBAC (internal/authz). Every route
// requires a verified OIDC subject (services/internal/auth.Middleware);
// every route below workspace creation additionally requires a real
// membership row, resolved fresh per request — never trusted from a
// header or cached in the token.
package api

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-chi/chi/v5"

	"github.com/dhiazfathra/how-to-replicate/services/capture-api/internal/sharelink"
	"github.com/dhiazfathra/how-to-replicate/services/capture-api/internal/store"
	"github.com/dhiazfathra/how-to-replicate/services/internal/auth"
	"github.com/dhiazfathra/how-to-replicate/services/internal/authz"
	"github.com/dhiazfathra/how-to-replicate/services/internal/db"
)

// NewRouter builds capture-api's authenticated chi router. verifier
// authenticates callers; st holds workspaces/projects/memberships/captures;
// pool is used directly by createWorkspace for the workspace+owner-membership
// transaction; signer mints/verifies share-link tokens for the
// createShareLink/revokeShareLink routes below (resolution itself is
// unauthenticated — see NewShareRouter).
func NewRouter(verifier *oidc.IDTokenVerifier, st *store.Store, pool db.Pool, signer sharelink.Signer) chi.Router {
	h := &handlers{store: st, pool: pool, signer: signer}

	r := chi.NewRouter()
	r.Use(auth.Middleware(verifier))

	r.Route("/v1/workspaces", func(r chi.Router) {
		r.Post("/", h.createWorkspace)
		r.Get("/", h.listWorkspaces)

		r.Route("/{workspaceID}", func(r chi.Router) {
			r.With(h.requirePermission(authz.PermissionCaptureRead)).Get("/", h.getWorkspace)
			r.With(h.requirePermission(authz.PermissionWorkspaceManage)).Patch("/", h.updateWorkspace)
			r.With(h.requirePermission(authz.PermissionWorkspaceManage)).Delete("/", h.deleteWorkspace)

			r.Route("/policy", func(r chi.Router) {
				r.With(h.requirePermission(authz.PermissionCaptureRead)).Get("/", h.getPolicy)
				// Owner-only, deliberately not authz.Can(role, ...): storage
				// caps, the LLM provider chain, and local-only are sensitive
				// enough (this repo's privacy-by-default posture, see
				// CLAUDE.md invariants) that even an admin shouldn't be able
				// to loosen them without the owner's say-so.
				r.With(h.requireOwner()).Put("/", h.updatePolicy)
			})

			r.Route("/projects", func(r chi.Router) {
				r.With(h.requirePermission(authz.PermissionProjectManage)).Post("/", h.createProject)
				r.With(h.requirePermission(authz.PermissionCaptureRead)).Get("/", h.listProjects)

				r.Route("/{projectID}", func(r chi.Router) {
					r.With(h.requirePermission(authz.PermissionCaptureRead)).Get("/", h.getProject)
					r.With(h.requirePermission(authz.PermissionProjectManage)).Patch("/", h.updateProject)
					r.With(h.requirePermission(authz.PermissionProjectManage)).Delete("/", h.deleteProject)
				})
			})

			// Captures: authenticated reads for share-link viewers' server-side
			// resolution and for a client whose local copy was evicted — not the
			// local-first UI's normal read path. See internal/api/captureview.go
			// for the ready-state gate every one of these routes through.
			r.Route("/captures", func(r chi.Router) {
				r.With(h.requirePermission(authz.PermissionCaptureRead)).Get("/", h.listCaptures)

				r.Route("/{captureID}", func(r chi.Router) {
					r.With(h.requirePermission(authz.PermissionCaptureRead)).Get("/", h.getCapture)
					r.With(h.requirePermission(authz.PermissionCaptureRead)).Get("/events", h.listEvents)
					r.With(h.requirePermission(authz.PermissionCaptureWrite)).Post("/comments", h.createComment)

					r.Route("/share-links", func(r chi.Router) {
						r.With(h.requirePermission(authz.PermissionCaptureWrite)).Post("/", h.createShareLink)
						r.With(h.requirePermission(authz.PermissionCaptureWrite)).Post("/{shareLinkID}/revoke", h.revokeShareLink)
					})
				})
			})
		})
	})

	return r
}

// NewShareRouter builds capture-api's unauthenticated share-link resolver.
// It is deliberately not mounted under auth.Middleware (share links are
// "no-login viewing", per the task brief) and is rate-limited per client
// IP so a token cannot be brute-forced.
func NewShareRouter(st *store.Store, signer sharelink.Signer, limiter *sharelink.Limiter) chi.Router {
	h := &shareHandler{store: st, signer: signer, limiter: limiter}
	r := chi.NewRouter()
	r.Get("/v1/share/{token}", h.resolve)
	return r
}

type handlers struct {
	store  *store.Store
	pool   db.Pool
	signer sharelink.Signer
}

// requirePermission resolves the caller's role in the {workspaceID} path
// param and rejects the request unless that role grants permission. A
// workspace the subject has no membership in — whether because it doesn't
// exist or because they were never added — gets the same 404 either way
// (RBAC's non-disclosure requirement); a real member lacking permission
// gets 403, since membership itself is already established at that point.
func (h *handlers) requirePermission(permission authz.Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			subject, ok := auth.Subject(r.Context())
			if !ok {
				http.Error(w, "unauthenticated", http.StatusUnauthorized)
				return
			}

			workspaceID := chi.URLParam(r, "workspaceID")
			role, ok, err := h.store.RoleInWorkspace(r.Context(), workspaceID, subject)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if !ok {
				http.NotFound(w, r)
				return
			}
			if !authz.Can(authz.Role(role), permission) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// requireOwner is like requirePermission but checks role == authz.RoleOwner
// exactly instead of authz.Can, for the one operation (policy overrides)
// that isn't modeled as a normal permission — see the comment on the PUT
// .../policy route.
func (h *handlers) requireOwner() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			subject, ok := auth.Subject(r.Context())
			if !ok {
				http.Error(w, "unauthenticated", http.StatusUnauthorized)
				return
			}

			workspaceID := chi.URLParam(r, "workspaceID")
			role, ok, err := h.store.RoleInWorkspace(r.Context(), workspaceID, subject)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if !ok {
				http.NotFound(w, r)
				return
			}
			if authz.Role(role) != authz.RoleOwner {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// newID mints a server-side opaque identifier for entities capture-api
// creates on the caller's behalf (workspaces, projects, memberships).
// This is deliberately not the client-minted-ULID convention the capture
// pipeline uses (identity for a *capture* is minted by the client that
// recorded it) — these rows have no client of record, capture-api is the
// only writer.
func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base32.HexEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
}

func handleStoreErr(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	http.Error(w, "internal error", http.StatusInternalServerError)
}
