package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/dhiazfathra/how-to-replicate/services/capture-api/internal/policy"
	"github.com/dhiazfathra/how-to-replicate/services/capture-api/internal/store"
	"github.com/dhiazfathra/how-to-replicate/services/internal/auth"
)

type workspaceRequest struct {
	Name string `json:"name"`
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

	ws, err := store.CreateWorkspaceWithOwner(r.Context(), h.pool, newID(), req.Name, newID(), subject)
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

	raw, err := json.Marshal(overrides)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	ws, err := h.store.SetPolicyOverrides(r.Context(), chi.URLParam(r, "workspaceID"), raw)
	if err != nil {
		handleStoreErr(w, r, err)
		return
	}

	var resolved policy.Overrides
	_ = json.Unmarshal(ws.PolicyOverrides, &resolved)
	writeJSON(w, http.StatusOK, policy.Resolve(policy.Defaults, resolved))
}
