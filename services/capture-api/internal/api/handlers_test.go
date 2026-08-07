package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/dhiazfathra/how-to-replicate/services/capture-api/internal/store"
	"github.com/dhiazfathra/how-to-replicate/services/internal/auth"
	"github.com/dhiazfathra/how-to-replicate/services/internal/authz"
	"github.com/dhiazfathra/how-to-replicate/services/internal/db/sqlcgen"
)

// fakeQuerier is a minimal sqlcgen.Querier double for api-level tests: it
// tracks a single workspace/project/membership fixture, keyed loosely
// enough for the handler flows under test.
type fakeQuerier struct {
	sqlcgen.Querier

	workspace  sqlcgen.Workspace
	project    sqlcgen.Project
	projects   []sqlcgen.Project
	membership sqlcgen.Membership
	memberOK   bool
	err        error

	capture       sqlcgen.Capture
	captureOK     bool
	captures      []sqlcgen.CaptureEvent
	comment       sqlcgen.Comment
	shareLink     sqlcgen.ShareLink
	shareLinkOK   bool
	auditLogCount *int
	eventsErr     error
	auditErr      error
}

func (f fakeQuerier) GetCaptureForWorkspace(context.Context, sqlcgen.GetCaptureForWorkspaceParams) (sqlcgen.Capture, error) {
	if f.err != nil {
		return sqlcgen.Capture{}, f.err
	}
	if !f.captureOK {
		return sqlcgen.Capture{}, pgx.ErrNoRows
	}
	return f.capture, nil
}
func (f fakeQuerier) GetCapture(context.Context, string) (sqlcgen.Capture, error) {
	if f.err != nil {
		return sqlcgen.Capture{}, f.err
	}
	if !f.captureOK {
		return sqlcgen.Capture{}, pgx.ErrNoRows
	}
	return f.capture, nil
}
func (f fakeQuerier) ListCapturesByWorkspace(context.Context, string) ([]sqlcgen.Capture, error) {
	if f.err != nil {
		return nil, f.err
	}
	if !f.captureOK {
		return nil, nil
	}
	return []sqlcgen.Capture{f.capture}, nil
}
func (f fakeQuerier) ListCaptureEventsByCapture(context.Context, string) ([]sqlcgen.CaptureEvent, error) {
	if f.eventsErr != nil {
		return nil, f.eventsErr
	}
	return f.captures, f.err
}
func (f fakeQuerier) CreateComment(context.Context, sqlcgen.CreateCommentParams) (sqlcgen.Comment, error) {
	return f.comment, f.err
}
func (f fakeQuerier) CreateShareLink(context.Context, sqlcgen.CreateShareLinkParams) (sqlcgen.ShareLink, error) {
	return f.shareLink, f.err
}
func (f fakeQuerier) GetShareLink(context.Context, string) (sqlcgen.ShareLink, error) {
	if f.err != nil {
		return sqlcgen.ShareLink{}, f.err
	}
	if !f.shareLinkOK {
		return sqlcgen.ShareLink{}, pgx.ErrNoRows
	}
	return f.shareLink, nil
}
func (f fakeQuerier) RevokeShareLink(context.Context, string) (sqlcgen.ShareLink, error) {
	return f.shareLink, f.err
}
func (f fakeQuerier) CreateAuditLog(context.Context, sqlcgen.CreateAuditLogParams) (sqlcgen.AuditLog, error) {
	if f.auditErr != nil {
		return sqlcgen.AuditLog{}, f.auditErr
	}
	if f.auditLogCount != nil {
		*f.auditLogCount++
	}
	return sqlcgen.AuditLog{}, f.err
}

func (f fakeQuerier) GetWorkspace(context.Context, string) (sqlcgen.Workspace, error) {
	return f.workspace, f.err
}
func (f fakeQuerier) UpdateWorkspace(context.Context, sqlcgen.UpdateWorkspaceParams) (sqlcgen.Workspace, error) {
	return f.workspace, f.err
}
func (f fakeQuerier) DeleteWorkspace(context.Context, string) error {
	return f.err
}
func (f fakeQuerier) UpdateWorkspacePolicyOverrides(context.Context, sqlcgen.UpdateWorkspacePolicyOverridesParams) (sqlcgen.Workspace, error) {
	return f.workspace, f.err
}
func (f fakeQuerier) ListWorkspacesForUser(context.Context, string) ([]sqlcgen.Workspace, error) {
	return []sqlcgen.Workspace{f.workspace}, f.err
}
func (f fakeQuerier) CreateProject(context.Context, sqlcgen.CreateProjectParams) (sqlcgen.Project, error) {
	return f.project, f.err
}
func (f fakeQuerier) GetProjectForWorkspace(context.Context, sqlcgen.GetProjectForWorkspaceParams) (sqlcgen.Project, error) {
	return f.project, f.err
}
func (f fakeQuerier) ListProjectsByWorkspace(context.Context, string) ([]sqlcgen.Project, error) {
	return f.projects, f.err
}
func (f fakeQuerier) UpdateProjectForWorkspace(context.Context, sqlcgen.UpdateProjectForWorkspaceParams) (sqlcgen.Project, error) {
	return f.project, f.err
}
func (f fakeQuerier) DeleteProjectForWorkspace(context.Context, sqlcgen.DeleteProjectForWorkspaceParams) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	return 1, nil
}
func (f fakeQuerier) GetMembershipByWorkspaceAndUser(context.Context, sqlcgen.GetMembershipByWorkspaceAndUserParams) (sqlcgen.Membership, error) {
	if f.err != nil {
		return sqlcgen.Membership{}, f.err
	}
	if !f.memberOK {
		return sqlcgen.Membership{}, pgx.ErrNoRows
	}
	return f.membership, nil
}

func newTestHandlers(fq fakeQuerier) *handlers {
	return &handlers{store: store.New(fq)}
}

func withChiParams(r *http.Request, params map[string]string) *http.Request {
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestGetWorkspace(t *testing.T) {
	ws := sqlcgen.Workspace{ID: "ws_1", Name: "Acme"}
	h := newTestHandlers(fakeQuerier{workspace: ws})

	r := httptest.NewRequest(http.MethodGet, "/v1/workspaces/ws_1", nil)
	r = withChiParams(r, map[string]string{"workspaceID": "ws_1"})
	rec := httptest.NewRecorder()

	h.getWorkspace(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got sqlcgen.Workspace
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.ID != "ws_1" {
		t.Fatalf("unexpected body: %s (err=%v)", rec.Body.String(), err)
	}
}

func TestCreateProject_InvalidBody(t *testing.T) {
	h := newTestHandlers(fakeQuerier{})
	r := httptest.NewRequest(http.MethodPost, "/v1/workspaces/ws_1/projects", strings.NewReader(`{}`))
	r = withChiParams(r, map[string]string{"workspaceID": "ws_1"})
	rec := httptest.NewRecorder()

	h.createProject(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestCreateProject_Success(t *testing.T) {
	p := sqlcgen.Project{ID: "p_1", WorkspaceID: "ws_1", Name: "Checkout"}
	h := newTestHandlers(fakeQuerier{project: p})
	r := httptest.NewRequest(http.MethodPost, "/v1/workspaces/ws_1/projects", strings.NewReader(`{"name":"Checkout"}`))
	r = withChiParams(r, map[string]string{"workspaceID": "ws_1"})
	rec := httptest.NewRecorder()

	h.createProject(rec, r)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteProject(t *testing.T) {
	h := newTestHandlers(fakeQuerier{})
	r := httptest.NewRequest(http.MethodDelete, "/v1/workspaces/ws_1/projects/p_1", nil)
	r = withChiParams(r, map[string]string{"workspaceID": "ws_1", "projectID": "p_1"})
	rec := httptest.NewRecorder()

	h.deleteProject(rec, r)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestGetPolicy_DefaultsWhenNoOverrides(t *testing.T) {
	h := newTestHandlers(fakeQuerier{workspace: sqlcgen.Workspace{ID: "ws_1"}})
	r := httptest.NewRequest(http.MethodGet, "/v1/workspaces/ws_1/policy", nil)
	r = withChiParams(r, map[string]string{"workspaceID": "ws_1"})
	rec := httptest.NewRecorder()

	h.getPolicy(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"localOnly":true`) {
		t.Fatalf("expected default localOnly=true, got %s", rec.Body.String())
	}
}

func TestUpdatePolicy(t *testing.T) {
	h := newTestHandlers(fakeQuerier{workspace: sqlcgen.Workspace{ID: "ws_1", PolicyOverrides: []byte(`{"localOnly":false}`)}})
	r := httptest.NewRequest(http.MethodPatch, "/v1/workspaces/ws_1/policy", strings.NewReader(`{"localOnly":false}`))
	r = withChiParams(r, map[string]string{"workspaceID": "ws_1"})
	rec := httptest.NewRecorder()

	h.updatePolicy(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"localOnly":false`) {
		t.Fatalf("expected overridden localOnly=false, got %s", rec.Body.String())
	}
}

func TestRequirePermission(t *testing.T) {
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
		{name: "insufficient role", subjectSet: true, memberOK: true, role: "viewer", wantStatus: http.StatusForbidden},
		{name: "sufficient role", subjectSet: true, memberOK: true, role: "owner", wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHandlers(fakeQuerier{memberOK: tt.memberOK, membership: sqlcgen.Membership{Role: tt.role}, err: tt.memberErr})

			var called bool
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			})

			r := httptest.NewRequest(http.MethodGet, "/v1/workspaces/ws_1", nil)
			r = withChiParams(r, map[string]string{"workspaceID": "ws_1"})
			if tt.subjectSet {
				r = r.WithContext(auth.WithSubject(r.Context(), "user-1"))
			}
			rec := httptest.NewRecorder()

			h.requirePermission(authz.PermissionMemberInvite)(next).ServeHTTP(rec, r)

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
