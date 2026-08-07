package store

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

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
