package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/dhiazfathra/how-to-replicate/services/capture-api/internal/policy"
	"github.com/dhiazfathra/how-to-replicate/services/capture-api/internal/store"
	"github.com/dhiazfathra/how-to-replicate/services/internal/auth"
)

type workspaceRequest struct {
	Name string `json:"name"`
	// RetentionDays is required: policy.Defaults has deliberately no
	// retention default (spec §22 open question 4 — the retention window
	// is a Security/Compliance judgment, not an engineering one), so a
	// workspace cannot come into existence without one being chosen here.
	RetentionDays int `json:"retentionDays"`
}

type projectRequest struct {
	Name string `json:"name"`
}

// createWorkspace makes the caller a workspace and its first owner.
// CreateWorkspaceWithOwner creates the workspace and the owner membership
// in one transaction, so a workspace is never observable without an owner
// able to manage it. This is the one workspace-scoped write that runs
// before any membership exists, so it isn't gated by requirePermission.
func (h *handlers) createWorkspace(w http.ResponseWriter, r *http.Request) {
	subject, ok := auth.Subject(r.Context())
	if !ok {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}

	var req workspaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	overrides := policy.Overrides{RetentionDays: &req.RetentionDays}
	if err := policy.Resolve(policy.Defaults, overrides).Validate(); err != nil {
		http.Error(w, "invalid request: "+err.Error(), http.StatusBadRequest)
		return
	}
	// policy.Overrides is a plain struct of pointers/slices to JSON-safe
	// scalars — Marshal on it cannot fail, same reasoning as updatePolicy
	// below.
	overridesJSON, _ := json.Marshal(overrides)

	ws, err := store.CreateWorkspaceWithOwner(r.Context(), h.pool, newID(), req.Name, newID(), subject, overridesJSON)
	if err != nil {
		handleStoreErr(w, r, err)
		return
	}

	writeJSON(w, http.StatusCreated, ws)
}

// listWorkspaces returns every workspace the caller is a member of.
func (h *handlers) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	subject, ok := auth.Subject(r.Context())
	if !ok {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}

	ws, err := h.store.ListWorkspacesForUser(r.Context(), subject)
	if err != nil {
		handleStoreErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ws)
}

func (h *handlers) getWorkspace(w http.ResponseWriter, r *http.Request) {
	ws, err := h.store.GetWorkspace(r.Context(), chi.URLParam(r, "workspaceID"))
	if err != nil {
		handleStoreErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ws)
}

func (h *handlers) updateWorkspace(w http.ResponseWriter, r *http.Request) {
	var req workspaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	ws, err := h.store.UpdateWorkspace(r.Context(), chi.URLParam(r, "workspaceID"), req.Name)
	if err != nil {
		handleStoreErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ws)
}

func (h *handlers) deleteWorkspace(w http.ResponseWriter, r *http.Request) {
	if err := h.store.DeleteWorkspace(r.Context(), chi.URLParam(r, "workspaceID")); err != nil {
		handleStoreErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) createProject(w http.ResponseWriter, r *http.Request) {
	var req projectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	p, err := h.store.CreateProject(r.Context(), newID(), chi.URLParam(r, "workspaceID"), req.Name)
	if err != nil {
		handleStoreErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (h *handlers) listProjects(w http.ResponseWriter, r *http.Request) {
	ps, err := h.store.ListProjects(r.Context(), chi.URLParam(r, "workspaceID"))
	if err != nil {
		handleStoreErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ps)
}

func (h *handlers) getProject(w http.ResponseWriter, r *http.Request) {
	p, err := h.store.GetProject(r.Context(), chi.URLParam(r, "projectID"), chi.URLParam(r, "workspaceID"))
	if err != nil {
		handleStoreErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *handlers) updateProject(w http.ResponseWriter, r *http.Request) {
	var req projectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	p, err := h.store.UpdateProject(r.Context(), chi.URLParam(r, "projectID"), chi.URLParam(r, "workspaceID"), req.Name)
	if err != nil {
		handleStoreErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *handlers) deleteProject(w http.ResponseWriter, r *http.Request) {
	if err := h.store.DeleteProject(r.Context(), chi.URLParam(r, "projectID"), chi.URLParam(r, "workspaceID")); err != nil {
		handleStoreErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// getPolicy returns the workspace's effective policy: hardcoded defaults
// with any stored per-workspace override layered on top.
func (h *handlers) getPolicy(w http.ResponseWriter, r *http.Request) {
	ws, err := h.store.GetWorkspace(r.Context(), chi.URLParam(r, "workspaceID"))
	if err != nil {
		handleStoreErr(w, r, err)
		return
	}

	var overrides policy.Overrides
	if len(ws.PolicyOverrides) > 0 {
		if err := json.Unmarshal(ws.PolicyOverrides, &overrides); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	writeJSON(w, http.StatusOK, policy.Resolve(policy.Defaults, overrides))
}

// updatePolicy replaces the workspace's stored policy overrides wholesale.
// Owner-only (requireOwner() on this route, checked against authz.RoleOwner
// directly rather than a permission an admin could also hold) — storage
// caps, the LLM provider chain, and local-only are sensitive enough that
// even an admin shouldn't be able to loosen them without the owner's
// say-so.
func (h *handlers) updatePolicy(w http.ResponseWriter, r *http.Request) {
	var overrides policy.Overrides
	if err := json.NewDecoder(r.Body).Decode(&overrides); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	// policy.Overrides is a plain struct of pointers/slices to JSON-safe
	// scalars — Marshal on it cannot fail, so there is no error branch here
	// to test or handle.
	raw, _ := json.Marshal(overrides)

	ws, err := h.store.SetPolicyOverrides(r.Context(), chi.URLParam(r, "workspaceID"), raw)
	if err != nil {
		handleStoreErr(w, r, err)
		return
	}

	var resolved policy.Overrides
	_ = json.Unmarshal(ws.PolicyOverrides, &resolved)
	writeJSON(w, http.StatusOK, policy.Resolve(policy.Defaults, resolved))
}

// getCapture is an authenticated read for share-link viewers' server-side
// resolution and for a client whose local copy was evicted — not the
// local-first UI's normal read path, which reads its own observable store.
// Every non-ready capture is gated down to status metadata by gateCapture,
// the one place invariant 1 ("no unredacted capture is viewable... the gate
// is state === 'ready'") is enforced.
func (h *handlers) getCapture(w http.ResponseWriter, r *http.Request) {
	subject, _ := auth.Subject(r.Context())
	c, err := h.store.GetCaptureAndAudit(r.Context(),
		chi.URLParam(r, "captureID"), chi.URLParam(r, "workspaceID"), subject, newID())
	if err != nil {
		handleStoreErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gateCapture(c))
}

// listCaptures returns every capture in the workspace, each individually
// gated through gateCapture — a non-ready capture in the list is status
// metadata only, same as a direct getCapture on it would be.
func (h *handlers) listCaptures(w http.ResponseWriter, r *http.Request) {
	cs, err := h.store.ListCaptures(r.Context(), chi.URLParam(r, "workspaceID"))
	if err != nil {
		handleStoreErr(w, r, err)
		return
	}
	views := make([]CaptureView, 0, len(cs))
	for _, c := range cs {
		views = append(views, gateCapture(c))
	}
	writeJSON(w, http.StatusOK, views)
}

// listEvents returns a capture's events, but only if the capture is ready
// (gateCaptureEvents) — for any other state it returns an empty list
// rather than the raw (possibly unredacted) event payloads.
func (h *handlers) listEvents(w http.ResponseWriter, r *http.Request) {
	captureID := chi.URLParam(r, "captureID")
	workspaceID := chi.URLParam(r, "workspaceID")

	c, err := h.store.GetCapture(r.Context(), captureID, workspaceID)
	if err != nil {
		handleStoreErr(w, r, err)
		return
	}
	evs, err := h.store.ListEvents(r.Context(), captureID, workspaceID)
	if err != nil {
		handleStoreErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, gateCaptureEvents(c, evs))
}

type commentRequest struct {
	Body string `json:"body"`
}

// createComment appends a comment to a capture, after the store verifies
// the capture belongs to this workspace (append-only, workspace-scoped —
// see internal/store.Store.CreateComment).
func (h *handlers) createComment(w http.ResponseWriter, r *http.Request) {
	subject, ok := auth.Subject(r.Context())
	if !ok {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}

	var req commentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Body == "" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	c, err := h.store.CreateComment(r.Context(), newID(),
		chi.URLParam(r, "captureID"), chi.URLParam(r, "workspaceID"), subject, req.Body)
	if err != nil {
		handleStoreErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

type shareLinkRequest struct {
	// ExpiresInSeconds is required and > 0: a share link with no expiry
	// would contradict "signed, expiring, revocable" (see task brief).
	ExpiresInSeconds int64 `json:"expiresInSeconds"`
}

type shareLinkResponse struct {
	ID        string    `json:"id"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// createShareLink mints a signed, expiring share link for a capture the
// caller's workspace owns. The signed token embeds the share link's own
// (random) ID as the revocation handle, not the capture ID — see
// internal/sharelink's package doc for why.
func (h *handlers) createShareLink(w http.ResponseWriter, r *http.Request) {
	subject, ok := auth.Subject(r.Context())
	if !ok {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}

	var req shareLinkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ExpiresInSeconds <= 0 {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	captureID := chi.URLParam(r, "captureID")
	workspaceID := chi.URLParam(r, "workspaceID")
	expiresAt := time.Now().Add(time.Duration(req.ExpiresInSeconds) * time.Second)

	id := newID()
	// sharelink.Signer.Sign only fails via json.Marshal on a plain struct of
	// strings/ints, which cannot fail (see its doc comment) — no error
	// branch here to test.
	token, _ := h.signer.Sign(id, captureID, expiresAt)

	sl, err := h.store.CreateShareLink(r.Context(), id, captureID, workspaceID, subject, token,
		pgtype.Timestamptz{Time: expiresAt, Valid: true})
	if err != nil {
		handleStoreErr(w, r, err)
		return
	}

	writeJSON(w, http.StatusCreated, shareLinkResponse{ID: sl.ID, Token: token, ExpiresAt: expiresAt})
}

// revokeShareLink revokes a share link belonging to a capture in the
// caller's workspace. Revocation is immediate: the resolver checks
// revoked_at on every resolution, so it does not depend on the token's own
// (independent) expiry.
func (h *handlers) revokeShareLink(w http.ResponseWriter, r *http.Request) {
	_, err := h.store.RevokeShareLink(r.Context(), chi.URLParam(r, "shareLinkID"), chi.URLParam(r, "workspaceID"))
	if err != nil {
		handleStoreErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
