// Package store wraps sqlcgen for capture-api's identity domain:
// workspaces, projects, users, and workspace-scoped memberships. Every
// project read/write is scoped by workspace_id so a project ID from
// another workspace behaves exactly like one that doesn't exist —
// pgx.ErrNoRows — which is what RBAC's non-disclosure requirement needs.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/dhiazfathra/how-to-replicate/services/internal/authz"
	"github.com/dhiazfathra/how-to-replicate/services/internal/db"
	"github.com/dhiazfathra/how-to-replicate/services/internal/db/sqlcgen"
)

// ErrNotFound is returned in place of pgx.ErrNoRows so callers don't need
// to import pgx to check for it.
var ErrNotFound = errors.New("store: not found")

// Store is the identity-domain persistence surface. It is constructed over
// sqlcgen.Querier so tests can fake it without a real database.
type Store struct {
	q sqlcgen.Querier
}

// New returns a Store backed by q.
func New(q sqlcgen.Querier) *Store {
	return &Store{q: q}
}

func wrapNotFound(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("store: %w", err)
}

// CreateWorkspace creates a workspace with the given client-minted ID.
func (s *Store) CreateWorkspace(ctx context.Context, id, name string) (sqlcgen.Workspace, error) {
	ws, err := s.q.CreateWorkspace(ctx, sqlcgen.CreateWorkspaceParams{ID: id, Name: name})
	return ws, wrapNotFound(err)
}

// GetWorkspace returns the workspace with id, or ErrNotFound.
func (s *Store) GetWorkspace(ctx context.Context, id string) (sqlcgen.Workspace, error) {
	ws, err := s.q.GetWorkspace(ctx, id)
	return ws, wrapNotFound(err)
}

// UpdateWorkspace renames the workspace with id, or returns ErrNotFound.
func (s *Store) UpdateWorkspace(ctx context.Context, id, name string) (sqlcgen.Workspace, error) {
	ws, err := s.q.UpdateWorkspace(ctx, sqlcgen.UpdateWorkspaceParams{ID: id, Name: name})
	return ws, wrapNotFound(err)
}

// DeleteWorkspace deletes the workspace with id.
func (s *Store) DeleteWorkspace(ctx context.Context, id string) error {
	return wrapNotFound(s.q.DeleteWorkspace(ctx, id))
}

// ListWorkspacesForUser returns every workspace userID is a member of,
// oldest first.
func (s *Store) ListWorkspacesForUser(ctx context.Context, userID string) ([]sqlcgen.Workspace, error) {
	ws, err := s.q.ListWorkspacesForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	return ws, nil
}

// GetWorkspaceForMember returns the workspace with id, but only if userID
// has a membership row for it — a workspace the caller isn't a member of
// is indistinguishable from one that doesn't exist (ErrNotFound either
// way), which is the non-disclosure property RBAC needs.
func (s *Store) GetWorkspaceForMember(ctx context.Context, id, userID string) (sqlcgen.Workspace, error) {
	ws, err := s.q.GetWorkspaceForMember(ctx, sqlcgen.GetWorkspaceForMemberParams{
		ID:     id,
		UserID: userID,
	})
	return ws, wrapNotFound(err)
}

// SetPolicyOverrides stores the raw JSON partial policy override object for
// workspaceID.
func (s *Store) SetPolicyOverrides(ctx context.Context, workspaceID string, overrides []byte) (sqlcgen.Workspace, error) {
	ws, err := s.q.UpdateWorkspacePolicyOverrides(ctx, sqlcgen.UpdateWorkspacePolicyOverridesParams{
		ID:              workspaceID,
		PolicyOverrides: overrides,
	})
	return ws, wrapNotFound(err)
}

// CreateWorkspaceWithOwner creates a workspace and grants ownerUserID the
// owner membership in the same transaction, so a workspace is never
// observable without at least one owner able to manage it.
func CreateWorkspaceWithOwner(ctx context.Context, pool db.Pool, workspaceID, name, membershipID, ownerUserID string) (sqlcgen.Workspace, error) {
	var ws sqlcgen.Workspace
	err := db.WithTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		s := New(sqlcgen.New(tx))
		var err error
		ws, err = s.CreateWorkspace(ctx, workspaceID, name)
		if err != nil {
			return err
		}
		_, err = s.CreateMembership(ctx, membershipID, workspaceID, ownerUserID, string(authz.RoleOwner))
		return err
	})
	return ws, err
}

// CreateProject creates a project in workspaceID with the given
// client-minted ID.
func (s *Store) CreateProject(ctx context.Context, id, workspaceID, name string) (sqlcgen.Project, error) {
	p, err := s.q.CreateProject(ctx, sqlcgen.CreateProjectParams{
		ID: id, WorkspaceID: workspaceID, Name: name,
	})
	return p, wrapNotFound(err)
}

// GetProject returns the project with id in workspaceID, or ErrNotFound if
// it doesn't exist or belongs to a different workspace.
func (s *Store) GetProject(ctx context.Context, id, workspaceID string) (sqlcgen.Project, error) {
	p, err := s.q.GetProjectForWorkspace(ctx, sqlcgen.GetProjectForWorkspaceParams{
		ID: id, WorkspaceID: workspaceID,
	})
	return p, wrapNotFound(err)
}

// ListProjects returns every project in workspaceID, oldest first.
func (s *Store) ListProjects(ctx context.Context, workspaceID string) ([]sqlcgen.Project, error) {
	ps, err := s.q.ListProjectsByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	return ps, nil
}

// UpdateProject renames the project with id in workspaceID, or returns
// ErrNotFound.
func (s *Store) UpdateProject(ctx context.Context, id, workspaceID, name string) (sqlcgen.Project, error) {
	p, err := s.q.UpdateProjectForWorkspace(ctx, sqlcgen.UpdateProjectForWorkspaceParams{
		ID: id, WorkspaceID: workspaceID, Name: name,
	})
	return p, wrapNotFound(err)
}

// DeleteProject deletes the project with id in workspaceID. It returns
// ErrNotFound if no row matched both id and workspaceID.
func (s *Store) DeleteProject(ctx context.Context, id, workspaceID string) error {
	n, err := s.q.DeleteProjectForWorkspace(ctx, sqlcgen.DeleteProjectForWorkspaceParams{
		ID: id, WorkspaceID: workspaceID,
	})
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpsertUser records or refreshes the local profile for an IdP-authenticated
// subject (id is the OIDC subject — see internal/auth).
func (s *Store) UpsertUser(ctx context.Context, id, email, name string) (sqlcgen.User, error) {
	u, err := s.q.UpsertUser(ctx, sqlcgen.UpsertUserParams{ID: id, Email: email, Name: name})
	return u, wrapNotFound(err)
}

// CreateMembership grants userID role in workspaceID.
func (s *Store) CreateMembership(ctx context.Context, id, workspaceID, userID, role string) (sqlcgen.Membership, error) {
	m, err := s.q.CreateMembership(ctx, sqlcgen.CreateMembershipParams{
		ID: id, WorkspaceID: workspaceID, UserID: userID, Role: role,
	})
	return m, wrapNotFound(err)
}

// RoleInWorkspace returns userID's role in workspaceID, and false if
// userID has no membership there (including when workspaceID itself
// doesn't exist — the two are deliberately indistinguishable here).
func (s *Store) RoleInWorkspace(ctx context.Context, workspaceID, userID string) (string, bool, error) {
	m, err := s.q.GetMembershipByWorkspaceAndUser(ctx, sqlcgen.GetMembershipByWorkspaceAndUserParams{
		WorkspaceID: workspaceID, UserID: userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("store: %w", err)
	}
	return m.Role, true, nil
}
