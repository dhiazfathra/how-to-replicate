package store

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/dhiazfathra/how-to-replicate/services/internal/db/sqlcgen"
)

// fakeQuerier implements sqlcgen.Querier by embedding it (nil); tests
// override only the methods Store actually calls. Calling an unoverridden
// method panics on the nil embedded interface, which is fine — it means a
// test exercised a path this fake wasn't built for.
type fakeQuerier struct {
	sqlcgen.Querier

	workspace   sqlcgen.Workspace
	workspaces  []sqlcgen.Workspace
	project     sqlcgen.Project
	projects    []sqlcgen.Project
	user        sqlcgen.User
	membership  sqlcgen.Membership
	deletedRows int64
	err         error

	capture    sqlcgen.Capture
	captureErr error
	captures   []sqlcgen.Capture
	events     []sqlcgen.CaptureEvent
	eventsErr  error
	comment    sqlcgen.Comment
	shareLink  sqlcgen.ShareLink
	shareErr   error
	revokeErr  error
	auditLog   sqlcgen.AuditLog
}

// captureErrOr returns captureErr if set, else the shared err field — most
// tests only need one error source, but RevokeShareLink's two-step
// GetShareLink-then-GetCapture flow needs to fail either step
// independently.
func (f fakeQuerier) captureErrOr() error {
	if f.captureErr != nil {
		return f.captureErr
	}
	return f.err
}

func (f fakeQuerier) GetCaptureForWorkspace(context.Context, sqlcgen.GetCaptureForWorkspaceParams) (sqlcgen.Capture, error) {
	return f.capture, f.captureErrOr()
}
func (f fakeQuerier) GetCapture(context.Context, string) (sqlcgen.Capture, error) {
	return f.capture, f.captureErrOr()
}
func (f fakeQuerier) ListCapturesByWorkspace(context.Context, string) ([]sqlcgen.Capture, error) {
	return f.captures, f.err
}
func (f fakeQuerier) ListCaptureEventsByCapture(context.Context, string) ([]sqlcgen.CaptureEvent, error) {
	if f.eventsErr != nil {
		return nil, f.eventsErr
	}
	return f.events, nil
}
func (f fakeQuerier) CreateComment(context.Context, sqlcgen.CreateCommentParams) (sqlcgen.Comment, error) {
	return f.comment, f.err
}
func (f fakeQuerier) CreateShareLink(context.Context, sqlcgen.CreateShareLinkParams) (sqlcgen.ShareLink, error) {
	return f.shareLink, f.err
}
func (f fakeQuerier) GetShareLink(context.Context, string) (sqlcgen.ShareLink, error) {
	if f.shareErr != nil {
		return f.shareLink, f.shareErr
	}
	return f.shareLink, f.err
}
func (f fakeQuerier) RevokeShareLink(context.Context, string) (sqlcgen.ShareLink, error) {
	if f.revokeErr != nil {
		return f.shareLink, f.revokeErr
	}
	return f.shareLink, f.err
}
func (f fakeQuerier) CreateAuditLog(context.Context, sqlcgen.CreateAuditLogParams) (sqlcgen.AuditLog, error) {
	return f.auditLog, f.err
}

func (f fakeQuerier) CreateWorkspace(context.Context, sqlcgen.CreateWorkspaceParams) (sqlcgen.Workspace, error) {
	return f.workspace, f.err
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
func (f fakeQuerier) ListWorkspacesForUser(context.Context, string) ([]sqlcgen.Workspace, error) {
	return f.workspaces, f.err
}
func (f fakeQuerier) GetWorkspaceForMember(context.Context, sqlcgen.GetWorkspaceForMemberParams) (sqlcgen.Workspace, error) {
	return f.workspace, f.err
}
func (f fakeQuerier) UpdateWorkspacePolicyOverrides(context.Context, sqlcgen.UpdateWorkspacePolicyOverridesParams) (sqlcgen.Workspace, error) {
	return f.workspace, f.err
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
	return f.deletedRows, f.err
}
func (f fakeQuerier) UpsertUser(context.Context, sqlcgen.UpsertUserParams) (sqlcgen.User, error) {
	return f.user, f.err
}
func (f fakeQuerier) CreateMembership(context.Context, sqlcgen.CreateMembershipParams) (sqlcgen.Membership, error) {
	return f.membership, f.err
}
func (f fakeQuerier) GetMembershipByWorkspaceAndUser(context.Context, sqlcgen.GetMembershipByWorkspaceAndUserParams) (sqlcgen.Membership, error) {
	return f.membership, f.err
}

func TestStore_WorkspaceCRUD(t *testing.T) {
	ws := sqlcgen.Workspace{ID: "ws_1", Name: "Acme"}
	s := New(fakeQuerier{workspace: ws})

	got, err := s.CreateWorkspace(t.Context(), "ws_1", "Acme")
	if err != nil || got.ID != ws.ID {
		t.Fatalf("CreateWorkspace() = (%v, %v)", got, err)
	}
	if got, err := s.GetWorkspace(t.Context(), "ws_1"); err != nil || got.ID != ws.ID {
		t.Fatalf("GetWorkspace() = (%v, %v)", got, err)
	}
	if got, err := s.UpdateWorkspace(t.Context(), "ws_1", "Acme Inc"); err != nil || got.ID != ws.ID {
		t.Fatalf("UpdateWorkspace() = (%v, %v)", got, err)
	}
	if err := s.DeleteWorkspace(t.Context(), "ws_1"); err != nil {
		t.Fatalf("DeleteWorkspace() = %v", err)
	}
}

func TestStore_NotFoundMapping(t *testing.T) {
	s := New(fakeQuerier{err: pgx.ErrNoRows})

	if _, err := s.GetWorkspace(t.Context(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetWorkspace() error = %v, want ErrNotFound", err)
	}
	if _, err := s.GetProject(t.Context(), "p", "ws"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetProject() error = %v, want ErrNotFound", err)
	}
}

func TestStore_OtherErrorsWrapped(t *testing.T) {
	boom := errors.New("boom")
	s := New(fakeQuerier{err: boom})

	if _, err := s.GetWorkspace(t.Context(), "ws"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("GetWorkspace() error = %v, want wrapped non-ErrNotFound", err)
	}
	if _, err := s.ListProjects(t.Context(), "ws"); err == nil {
		t.Fatalf("ListProjects() want error")
	}
}

func TestStore_ProjectCRUD(t *testing.T) {
	p := sqlcgen.Project{ID: "p_1", WorkspaceID: "ws_1", Name: "Checkout"}
	s := New(fakeQuerier{project: p, projects: []sqlcgen.Project{p}})

	if got, err := s.CreateProject(t.Context(), "p_1", "ws_1", "Checkout"); err != nil || got != p {
		t.Fatalf("CreateProject() = (%v, %v)", got, err)
	}
	if got, err := s.GetProject(t.Context(), "p_1", "ws_1"); err != nil || got != p {
		t.Fatalf("GetProject() = (%v, %v)", got, err)
	}
	if got, err := s.ListProjects(t.Context(), "ws_1"); err != nil || len(got) != 1 {
		t.Fatalf("ListProjects() = (%v, %v)", got, err)
	}
	if got, err := s.UpdateProject(t.Context(), "p_1", "ws_1", "Checkout v2"); err != nil || got != p {
		t.Fatalf("UpdateProject() = (%v, %v)", got, err)
	}
}

func TestStore_DeleteProject(t *testing.T) {
	t.Run("deleted", func(t *testing.T) {
		s := New(fakeQuerier{deletedRows: 1})
		if err := s.DeleteProject(t.Context(), "p", "ws"); err != nil {
			t.Fatalf("DeleteProject() = %v", err)
		}
	})
	t.Run("no matching row", func(t *testing.T) {
		s := New(fakeQuerier{deletedRows: 0})
		if err := s.DeleteProject(t.Context(), "p", "ws"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("DeleteProject() = %v, want ErrNotFound", err)
		}
	})
	t.Run("query error", func(t *testing.T) {
		s := New(fakeQuerier{err: errors.New("boom")})
		if err := s.DeleteProject(t.Context(), "p", "ws"); err == nil {
			t.Fatalf("DeleteProject() want error")
		}
	})
}

func TestStore_UpsertUserAndMembership(t *testing.T) {
	u := sqlcgen.User{ID: "user-1", Email: "a@b.com", Name: "A"}
	m := sqlcgen.Membership{ID: "m_1", Role: "owner"}
	s := New(fakeQuerier{user: u, membership: m})

	if got, err := s.UpsertUser(t.Context(), "user-1", "a@b.com", "A"); err != nil || got != u {
		t.Fatalf("UpsertUser() = (%v, %v)", got, err)
	}
	if got, err := s.CreateMembership(t.Context(), "m_1", "ws_1", "user-1", "owner"); err != nil || got != m {
		t.Fatalf("CreateMembership() = (%v, %v)", got, err)
	}
}

func TestStore_RoleInWorkspace(t *testing.T) {
	t.Run("member", func(t *testing.T) {
		s := New(fakeQuerier{membership: sqlcgen.Membership{Role: "viewer"}})
		role, ok, err := s.RoleInWorkspace(t.Context(), "ws", "user")
		if err != nil || !ok || role != "viewer" {
			t.Fatalf("RoleInWorkspace() = (%q, %v, %v)", role, ok, err)
		}
	})
	t.Run("no membership", func(t *testing.T) {
		s := New(fakeQuerier{err: pgx.ErrNoRows})
		_, ok, err := s.RoleInWorkspace(t.Context(), "ws", "user")
		if err != nil || ok {
			t.Fatalf("RoleInWorkspace() = (ok=%v, err=%v), want (false, nil)", ok, err)
		}
	})
	t.Run("query error", func(t *testing.T) {
		s := New(fakeQuerier{err: errors.New("boom")})
		_, ok, err := s.RoleInWorkspace(t.Context(), "ws", "user")
		if err == nil || ok {
			t.Fatalf("RoleInWorkspace() = (ok=%v, err=%v), want an error", ok, err)
		}
	})
}

func TestStore_ListWorkspacesAndGetForMember(t *testing.T) {
	ws := sqlcgen.Workspace{ID: "ws_1"}
	s := New(fakeQuerier{workspaces: []sqlcgen.Workspace{ws}, workspace: ws})

	if got, err := s.ListWorkspacesForUser(t.Context(), "user"); err != nil || len(got) != 1 {
		t.Fatalf("ListWorkspacesForUser() = (%v, %v)", got, err)
	}
	if got, err := s.GetWorkspaceForMember(t.Context(), "ws_1", "user"); err != nil || got.ID != ws.ID {
		t.Fatalf("GetWorkspaceForMember() = (%v, %v)", got, err)
	}
}

func TestStore_ListWorkspacesForUser_Error(t *testing.T) {
	s := New(fakeQuerier{err: errors.New("boom")})

	if _, err := s.ListWorkspacesForUser(t.Context(), "user"); err == nil {
		t.Fatal("ListWorkspacesForUser() error = nil, want error")
	}
}

func TestStore_SetPolicyOverrides(t *testing.T) {
	ws := sqlcgen.Workspace{ID: "ws_1", PolicyOverrides: []byte(`{"localOnly":false}`)}
	s := New(fakeQuerier{workspace: ws})

	got, err := s.SetPolicyOverrides(t.Context(), "ws_1", []byte(`{"localOnly":false}`))
	if err != nil || got.ID != ws.ID {
		t.Fatalf("SetPolicyOverrides() = (%v, %v)", got, err)
	}
}

func TestStore_GetCapture(t *testing.T) {
	c := sqlcgen.Capture{ID: "cap_1", WorkspaceID: "ws_1"}
	s := New(fakeQuerier{capture: c})

	got, err := s.GetCapture(t.Context(), "cap_1", "ws_1")
	if err != nil || got.ID != c.ID {
		t.Fatalf("GetCapture() = (%v, %v)", got, err)
	}

	s = New(fakeQuerier{captureErr: pgx.ErrNoRows})
	if _, err := s.GetCapture(t.Context(), "missing", "ws_1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetCapture() error = %v, want ErrNotFound", err)
	}
}

func TestStore_GetCaptureByID(t *testing.T) {
	c := sqlcgen.Capture{ID: "cap_1"}
	s := New(fakeQuerier{capture: c})

	got, err := s.GetCaptureByID(t.Context(), "cap_1")
	if err != nil || got.ID != c.ID {
		t.Fatalf("GetCaptureByID() = (%v, %v)", got, err)
	}

	s = New(fakeQuerier{captureErr: pgx.ErrNoRows})
	if _, err := s.GetCaptureByID(t.Context(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetCaptureByID() error = %v, want ErrNotFound", err)
	}
}

func TestStore_ListCaptures(t *testing.T) {
	cs := []sqlcgen.Capture{{ID: "cap_1"}}
	s := New(fakeQuerier{captures: cs})

	got, err := s.ListCaptures(t.Context(), "ws_1")
	if err != nil || len(got) != 1 {
		t.Fatalf("ListCaptures() = (%v, %v)", got, err)
	}

	s = New(fakeQuerier{err: errors.New("boom")})
	if _, err := s.ListCaptures(t.Context(), "ws_1"); err == nil {
		t.Fatal("ListCaptures() want error")
	}
}

func TestStore_ListEvents(t *testing.T) {
	evs := []sqlcgen.CaptureEvent{{ID: "ev_1"}}
	s := New(fakeQuerier{events: evs})

	got, err := s.ListEvents(t.Context(), "cap_1", "ws_1")
	if err != nil || len(got) != 1 {
		t.Fatalf("ListEvents() = (%v, %v)", got, err)
	}

	t.Run("capture not in workspace", func(t *testing.T) {
		s := New(fakeQuerier{captureErr: pgx.ErrNoRows})
		if _, err := s.ListEvents(t.Context(), "cap_1", "ws_1"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("ListEvents() error = %v, want ErrNotFound", err)
		}
	})

	t.Run("query error", func(t *testing.T) {
		s := New(fakeQuerier{eventsErr: errors.New("boom")})
		if _, err := s.ListEvents(t.Context(), "cap_1", "ws_1"); err == nil {
			t.Fatal("ListEvents() want error")
		}
	})
}

func TestStore_CreateComment(t *testing.T) {
	c := sqlcgen.Comment{ID: "cm_1", CaptureID: "cap_1"}
	s := New(fakeQuerier{comment: c})

	got, err := s.CreateComment(t.Context(), "cm_1", "cap_1", "ws_1", "user-1", "hi")
	if err != nil || got.ID != c.ID {
		t.Fatalf("CreateComment() = (%v, %v)", got, err)
	}

	t.Run("capture not in workspace", func(t *testing.T) {
		s := New(fakeQuerier{captureErr: pgx.ErrNoRows})
		if _, err := s.CreateComment(t.Context(), "cm_1", "cap_1", "ws_1", "user-1", "hi"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("CreateComment() error = %v, want ErrNotFound", err)
		}
	})
}

func TestStore_CreateShareLink(t *testing.T) {
	sl := sqlcgen.ShareLink{ID: "sl_1", CaptureID: "cap_1"}
	s := New(fakeQuerier{shareLink: sl})

	got, err := s.CreateShareLink(t.Context(), "sl_1", "cap_1", "ws_1", "user-1", "token", pgtype.Timestamptz{})
	if err != nil || got.ID != sl.ID {
		t.Fatalf("CreateShareLink() = (%v, %v)", got, err)
	}

	t.Run("capture not in workspace", func(t *testing.T) {
		s := New(fakeQuerier{captureErr: pgx.ErrNoRows})
		if _, err := s.CreateShareLink(t.Context(), "sl_1", "cap_1", "ws_1", "user-1", "token", pgtype.Timestamptz{}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("CreateShareLink() error = %v, want ErrNotFound", err)
		}
	})
}

func TestStore_RevokeShareLink(t *testing.T) {
	sl := sqlcgen.ShareLink{ID: "sl_1", CaptureID: "cap_1"}
	s := New(fakeQuerier{shareLink: sl})

	got, err := s.RevokeShareLink(t.Context(), "sl_1", "ws_1")
	if err != nil || got.ID != sl.ID {
		t.Fatalf("RevokeShareLink() = (%v, %v)", got, err)
	}

	t.Run("share link not found", func(t *testing.T) {
		s := New(fakeQuerier{shareErr: pgx.ErrNoRows})
		if _, err := s.RevokeShareLink(t.Context(), "sl_1", "ws_1"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("RevokeShareLink() error = %v, want ErrNotFound", err)
		}
	})

	t.Run("capture not in workspace", func(t *testing.T) {
		s := New(fakeQuerier{shareLink: sl, captureErr: pgx.ErrNoRows})
		if _, err := s.RevokeShareLink(t.Context(), "sl_1", "ws_1"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("RevokeShareLink() error = %v, want ErrNotFound", err)
		}
	})

	t.Run("revoke query error", func(t *testing.T) {
		s := New(fakeQuerier{shareLink: sl, revokeErr: errors.New("boom")})
		if _, err := s.RevokeShareLink(t.Context(), "sl_1", "ws_1"); err == nil {
			t.Fatal("RevokeShareLink() want error")
		}
	})
}

func TestStore_GetShareLink(t *testing.T) {
	sl := sqlcgen.ShareLink{ID: "sl_1"}
	s := New(fakeQuerier{shareLink: sl})

	got, err := s.GetShareLink(t.Context(), "sl_1")
	if err != nil || got.ID != sl.ID {
		t.Fatalf("GetShareLink() = (%v, %v)", got, err)
	}

	s = New(fakeQuerier{shareErr: pgx.ErrNoRows})
	if _, err := s.GetShareLink(t.Context(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetShareLink() error = %v, want ErrNotFound", err)
	}
}

func TestStore_CreateAuditLog(t *testing.T) {
	al := sqlcgen.AuditLog{ID: "al_1"}
	s := New(fakeQuerier{auditLog: al})

	got, err := s.CreateAuditLog(t.Context(), "al_1", "ws_1", "user-1", "share_link.resolve", "cap_1", []byte(`{}`))
	if err != nil || got.ID != al.ID {
		t.Fatalf("CreateAuditLog() = (%v, %v)", got, err)
	}

	// actorID == "" is the unauthenticated share-link-resolver path — it
	// must map to a NULL actor_id (pgtype.Text.Valid == false), not the
	// literal empty string, which is exercised indirectly here via a
	// no-error round trip.
	if _, err := s.CreateAuditLog(t.Context(), "al_1", "ws_1", "", "share_link.resolve", "cap_1", []byte(`{}`)); err != nil {
		t.Fatalf("CreateAuditLog() with empty actorID = %v", err)
	}

	s = New(fakeQuerier{err: errors.New("boom")})
	if _, err := s.CreateAuditLog(t.Context(), "al_1", "ws_1", "user-1", "share_link.resolve", "cap_1", nil); err == nil {
		t.Fatal("CreateAuditLog() want error")
	}
}
