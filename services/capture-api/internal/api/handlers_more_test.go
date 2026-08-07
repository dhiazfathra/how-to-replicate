package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/dhiazfathra/how-to-replicate/services/capture-api/internal/sharelink"
	"github.com/dhiazfathra/how-to-replicate/services/internal/auth"
	"github.com/dhiazfathra/how-to-replicate/services/internal/db/sqlcgen"
)

func withSubject(r *http.Request, subject string) *http.Request {
	return r.WithContext(auth.WithSubject(r.Context(), subject))
}

func TestCreateWorkspace(t *testing.T) {
	t.Run("unauthenticated", func(t *testing.T) {
		h := newTestHandlers(fakeQuerier{})
		r := httptest.NewRequest(http.MethodPost, "/v1/workspaces", strings.NewReader(`{"name":"Acme"}`))
		rec := httptest.NewRecorder()

		h.createWorkspace(rec, r)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("invalid body", func(t *testing.T) {
		h := newTestHandlers(fakeQuerier{})
		r := withSubject(httptest.NewRequest(http.MethodPost, "/v1/workspaces", strings.NewReader(`{}`)), "user-1")
		rec := httptest.NewRecorder()

		h.createWorkspace(rec, r)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("store error", func(t *testing.T) {
		h := newTestHandlers(fakeQuerier{})
		h.pool = &fakePool{beginErr: errors.New("boom")}
		r := withSubject(httptest.NewRequest(http.MethodPost, "/v1/workspaces", strings.NewReader(`{"name":"Acme"}`)), "user-1")
		rec := httptest.NewRecorder()

		h.createWorkspace(rec, r)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500, body = %s", rec.Code, rec.Body.String())
		}
	})
}

// fakePool is a minimal db.Pool that fails at Begin, letting tests exercise
// createWorkspace's store-error branch without a real database transaction.
type fakePool struct {
	beginErr error
}

func (f *fakePool) Ping(context.Context) error            { return nil }
func (f *fakePool) Begin(context.Context) (pgx.Tx, error) { return nil, f.beginErr }
func (f *fakePool) Close()                                {}

func TestGetWorkspace_Error(t *testing.T) {
	h := newTestHandlers(fakeQuerier{err: errors.New("boom")})
	r := httptest.NewRequest(http.MethodGet, "/v1/workspaces/ws_1", nil)
	r = withChiParams(r, map[string]string{"workspaceID": "ws_1"})
	rec := httptest.NewRecorder()

	h.getWorkspace(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestCreateProject_StoreError(t *testing.T) {
	h := newTestHandlers(fakeQuerier{err: errors.New("boom")})
	r := httptest.NewRequest(http.MethodPost, "/v1/workspaces/ws_1/projects", strings.NewReader(`{"name":"Checkout"}`))
	r = withChiParams(r, map[string]string{"workspaceID": "ws_1"})
	rec := httptest.NewRecorder()

	h.createProject(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestListWorkspaces(t *testing.T) {
	t.Run("unauthenticated", func(t *testing.T) {
		h := newTestHandlers(fakeQuerier{})
		r := httptest.NewRequest(http.MethodGet, "/v1/workspaces", nil)
		rec := httptest.NewRecorder()

		h.listWorkspaces(rec, r)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("success", func(t *testing.T) {
		h := newTestHandlers(fakeQuerier{workspace: sqlcgen.Workspace{ID: "ws_1"}})
		r := withSubject(httptest.NewRequest(http.MethodGet, "/v1/workspaces", nil), "user-1")
		rec := httptest.NewRecorder()

		h.listWorkspaces(rec, r)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("store error", func(t *testing.T) {
		h := newTestHandlers(fakeQuerier{err: errors.New("boom")})
		r := withSubject(httptest.NewRequest(http.MethodGet, "/v1/workspaces", nil), "user-1")
		rec := httptest.NewRecorder()

		h.listWorkspaces(rec, r)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})
}

func TestUpdateWorkspace(t *testing.T) {
	t.Run("invalid body", func(t *testing.T) {
		h := newTestHandlers(fakeQuerier{})
		r := httptest.NewRequest(http.MethodPatch, "/v1/workspaces/ws_1", strings.NewReader(`{}`))
		r = withChiParams(r, map[string]string{"workspaceID": "ws_1"})
		rec := httptest.NewRecorder()

		h.updateWorkspace(rec, r)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("success", func(t *testing.T) {
		h := newTestHandlers(fakeQuerier{workspace: sqlcgen.Workspace{ID: "ws_1", Name: "Acme Inc"}})
		r := httptest.NewRequest(http.MethodPatch, "/v1/workspaces/ws_1", strings.NewReader(`{"name":"Acme Inc"}`))
		r = withChiParams(r, map[string]string{"workspaceID": "ws_1"})
		rec := httptest.NewRecorder()

		h.updateWorkspace(rec, r)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("not found", func(t *testing.T) {
		h := newTestHandlers(fakeQuerier{err: errors.New("boom")})
		r := httptest.NewRequest(http.MethodPatch, "/v1/workspaces/ws_1", strings.NewReader(`{"name":"Acme Inc"}`))
		r = withChiParams(r, map[string]string{"workspaceID": "ws_1"})
		rec := httptest.NewRecorder()

		h.updateWorkspace(rec, r)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})
}

func TestDeleteWorkspace(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		h := newTestHandlers(fakeQuerier{})
		r := httptest.NewRequest(http.MethodDelete, "/v1/workspaces/ws_1", nil)
		r = withChiParams(r, map[string]string{"workspaceID": "ws_1"})
		rec := httptest.NewRecorder()

		h.deleteWorkspace(rec, r)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d", rec.Code)
		}
	})

	t.Run("error", func(t *testing.T) {
		h := newTestHandlers(fakeQuerier{err: errors.New("boom")})
		r := httptest.NewRequest(http.MethodDelete, "/v1/workspaces/ws_1", nil)
		r = withChiParams(r, map[string]string{"workspaceID": "ws_1"})
		rec := httptest.NewRecorder()

		h.deleteWorkspace(rec, r)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})
}

func TestListProjects(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		h := newTestHandlers(fakeQuerier{projects: []sqlcgen.Project{{ID: "p_1"}}})
		r := httptest.NewRequest(http.MethodGet, "/v1/workspaces/ws_1/projects", nil)
		r = withChiParams(r, map[string]string{"workspaceID": "ws_1"})
		rec := httptest.NewRecorder()

		h.listProjects(rec, r)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("error", func(t *testing.T) {
		h := newTestHandlers(fakeQuerier{err: errors.New("boom")})
		r := httptest.NewRequest(http.MethodGet, "/v1/workspaces/ws_1/projects", nil)
		r = withChiParams(r, map[string]string{"workspaceID": "ws_1"})
		rec := httptest.NewRecorder()

		h.listProjects(rec, r)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})
}

func TestGetProject(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		h := newTestHandlers(fakeQuerier{project: sqlcgen.Project{ID: "p_1"}})
		r := httptest.NewRequest(http.MethodGet, "/v1/workspaces/ws_1/projects/p_1", nil)
		r = withChiParams(r, map[string]string{"workspaceID": "ws_1", "projectID": "p_1"})
		rec := httptest.NewRecorder()

		h.getProject(rec, r)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("not found", func(t *testing.T) {
		h := newTestHandlers(fakeQuerier{err: errors.New("boom")})
		r := httptest.NewRequest(http.MethodGet, "/v1/workspaces/ws_1/projects/p_1", nil)
		r = withChiParams(r, map[string]string{"workspaceID": "ws_1", "projectID": "p_1"})
		rec := httptest.NewRecorder()

		h.getProject(rec, r)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})
}

func TestUpdateProject(t *testing.T) {
	t.Run("invalid body", func(t *testing.T) {
		h := newTestHandlers(fakeQuerier{})
		r := httptest.NewRequest(http.MethodPatch, "/v1/workspaces/ws_1/projects/p_1", strings.NewReader(`{}`))
		r = withChiParams(r, map[string]string{"workspaceID": "ws_1", "projectID": "p_1"})
		rec := httptest.NewRecorder()

		h.updateProject(rec, r)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("success", func(t *testing.T) {
		h := newTestHandlers(fakeQuerier{project: sqlcgen.Project{ID: "p_1", Name: "Checkout v2"}})
		r := httptest.NewRequest(http.MethodPatch, "/v1/workspaces/ws_1/projects/p_1", strings.NewReader(`{"name":"Checkout v2"}`))
		r = withChiParams(r, map[string]string{"workspaceID": "ws_1", "projectID": "p_1"})
		rec := httptest.NewRecorder()

		h.updateProject(rec, r)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("not found", func(t *testing.T) {
		h := newTestHandlers(fakeQuerier{err: errors.New("boom")})
		r := httptest.NewRequest(http.MethodPatch, "/v1/workspaces/ws_1/projects/p_1", strings.NewReader(`{"name":"Checkout v2"}`))
		r = withChiParams(r, map[string]string{"workspaceID": "ws_1", "projectID": "p_1"})
		rec := httptest.NewRecorder()

		h.updateProject(rec, r)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})
}

func TestDeleteProject_Error(t *testing.T) {
	h := newTestHandlers(fakeQuerier{err: errors.New("boom")})
	r := httptest.NewRequest(http.MethodDelete, "/v1/workspaces/ws_1/projects/p_1", nil)
	r = withChiParams(r, map[string]string{"workspaceID": "ws_1", "projectID": "p_1"})
	rec := httptest.NewRecorder()

	h.deleteProject(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestGetPolicy_StoreError(t *testing.T) {
	h := newTestHandlers(fakeQuerier{err: errors.New("boom")})
	r := httptest.NewRequest(http.MethodGet, "/v1/workspaces/ws_1/policy", nil)
	r = withChiParams(r, map[string]string{"workspaceID": "ws_1"})
	rec := httptest.NewRecorder()

	h.getPolicy(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestGetPolicy_InvalidStoredOverrides(t *testing.T) {
	h := newTestHandlers(fakeQuerier{workspace: sqlcgen.Workspace{ID: "ws_1", PolicyOverrides: []byte(`not json`)}})
	r := httptest.NewRequest(http.MethodGet, "/v1/workspaces/ws_1/policy", nil)
	r = withChiParams(r, map[string]string{"workspaceID": "ws_1"})
	rec := httptest.NewRecorder()

	h.getPolicy(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestUpdatePolicy_InvalidBody(t *testing.T) {
	h := newTestHandlers(fakeQuerier{})
	r := httptest.NewRequest(http.MethodPut, "/v1/workspaces/ws_1/policy", strings.NewReader(`not json`))
	r = withChiParams(r, map[string]string{"workspaceID": "ws_1"})
	rec := httptest.NewRecorder()

	h.updatePolicy(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestUpdatePolicy_StoreError(t *testing.T) {
	h := newTestHandlers(fakeQuerier{err: errors.New("boom")})
	r := httptest.NewRequest(http.MethodPut, "/v1/workspaces/ws_1/policy", strings.NewReader(`{}`))
	r = withChiParams(r, map[string]string{"workspaceID": "ws_1"})
	rec := httptest.NewRecorder()

	h.updatePolicy(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

// TestRequireOwner exercises the owner-only gate used by PUT .../policy.
func TestRequireOwner(t *testing.T) {
	tests := []struct {
		name       string
		subjectSet bool
		memberOK   bool
		memberErr  error
		role       string
		wantStatus int
	}{
		{name: "unauthenticated", wantStatus: http.StatusUnauthorized},
		{name: "store error", subjectSet: true, memberErr: errors.New("boom"), wantStatus: http.StatusInternalServerError},
		{name: "not a member", subjectSet: true, memberOK: false, wantStatus: http.StatusNotFound},
		{name: "admin is not owner", subjectSet: true, memberOK: true, role: "admin", wantStatus: http.StatusForbidden},
		{name: "owner", subjectSet: true, memberOK: true, role: "owner", wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHandlers(fakeQuerier{
				memberOK:   tt.memberOK,
				membership: sqlcgen.Membership{Role: tt.role},
				err:        tt.memberErr,
			})

			var called bool
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			})

			r := httptest.NewRequest(http.MethodPut, "/v1/workspaces/ws_1/policy", nil)
			r = withChiParams(r, map[string]string{"workspaceID": "ws_1"})
			if tt.subjectSet {
				r = withSubject(r, "user-1")
			}
			rec := httptest.NewRecorder()

			h.requireOwner()(next).ServeHTTP(rec, r)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			wantCalled := tt.wantStatus == http.StatusOK
			if called != wantCalled {
				t.Fatalf("next called = %v, want %v", called, wantCalled)
			}
		})
	}
}

// TestNewRouter smoke-tests that the route tree builds without panicking.
func TestNewRouter(t *testing.T) {
	st := newTestHandlers(fakeQuerier{}).store
	NewRouter(nil, st, nil, sharelink.Signer{Current: sharelink.Key{ID: "k1", Secret: []byte("secret")}})
}

func TestHandleStoreErr(t *testing.T) {
	t.Run("not found", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handleStoreErr(rec, httptest.NewRequest(http.MethodGet, "/", nil), errWrap())
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("other error", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handleStoreErr(rec, httptest.NewRequest(http.MethodGet, "/", nil), errors.New("boom"))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})
}

// errWrap returns store.ErrNotFound via the store package's fake-driven
// path (GetWorkspace on a fakeQuerier seeded with pgx.ErrNoRows), keeping
// this test from importing store.ErrNotFound directly for a trivial check.
func errWrap() error {
	_, err := newTestHandlers(fakeQuerier{err: pgx.ErrNoRows}).store.GetWorkspace(context.Background(), "missing")
	return err
}
