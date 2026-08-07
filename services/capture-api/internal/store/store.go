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
	"github.com/jackc/pgx/v5/pgtype"

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

// CreateWorkspaceWithOwner creates a workspace, stores its policy
// overrides, and grants ownerUserID the owner membership — all in the same
// transaction, so a workspace is never observable without at least one
// owner able to manage it, and never observable without a retention window
// already chosen (policyOverrides must resolve to a positive RetentionDays
// per policy.Defaults having none — see internal/policy — but validating
// the resolved value is the caller's job, since only the caller knows
// policy.Defaults; this just stores whatever bytes it's given atomically
// with creation).
func CreateWorkspaceWithOwner(ctx context.Context, pool db.Pool, workspaceID, name, membershipID, ownerUserID string, policyOverrides []byte) (sqlcgen.Workspace, error) {
	var ws sqlcgen.Workspace
	err := db.WithTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		s := New(sqlcgen.New(tx))
		var err error
		ws, err = s.CreateWorkspace(ctx, workspaceID, name)
		if err != nil {
			return err
		}
		ws, err = s.SetPolicyOverrides(ctx, workspaceID, policyOverrides)
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

// GetCapture returns the capture with id in workspaceID, or ErrNotFound if
// it doesn't exist or belongs to a different workspace — same
// no-existence-leak shape as GetProject.
func (s *Store) GetCapture(ctx context.Context, id, workspaceID string) (sqlcgen.Capture, error) {
	c, err := s.q.GetCaptureForWorkspace(ctx, sqlcgen.GetCaptureForWorkspaceParams{
		ID: id, WorkspaceID: workspaceID,
	})
	return c, wrapNotFound(err)
}

// GetCaptureByID returns the capture with id, with no workspace scoping.
// Used only by the (unauthenticated) share-link resolver, which has no
// workspace context to scope by — the token's signature plus the
// revocation/ready checks are what gate access there instead.
func (s *Store) GetCaptureByID(ctx context.Context, id string) (sqlcgen.Capture, error) {
	c, err := s.q.GetCapture(ctx, id)
	return c, wrapNotFound(err)
}

// ListCaptures returns every capture in workspaceID, oldest first.
func (s *Store) ListCaptures(ctx context.Context, workspaceID string) ([]sqlcgen.Capture, error) {
	cs, err := s.q.ListCapturesByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	return cs, nil
}

// ListEvents returns every event for captureID, in timeline order, but only
// if captureID belongs to workspaceID — same workspace-scoping requirement
// as GetCapture.
func (s *Store) ListEvents(ctx context.Context, captureID, workspaceID string) ([]sqlcgen.CaptureEvent, error) {
	if _, err := s.GetCapture(ctx, captureID, workspaceID); err != nil {
		return nil, err
	}
	evs, err := s.q.ListCaptureEventsByCapture(ctx, captureID)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	return evs, nil
}

// CreateComment appends a comment to captureID, after verifying captureID
// belongs to workspaceID (comments are append-only and workspace-scoped —
// a capture ID from another workspace must behave like it doesn't exist).
func (s *Store) CreateComment(ctx context.Context, id, captureID, workspaceID, authorID, body string) (sqlcgen.Comment, error) {
	if _, err := s.GetCapture(ctx, captureID, workspaceID); err != nil {
		return sqlcgen.Comment{}, err
	}
	c, err := s.q.CreateComment(ctx, sqlcgen.CreateCommentParams{
		ID: id, CaptureID: captureID, AuthorID: authorID, Body: body,
	})
	return c, wrapNotFound(err)
}

// CreateShareLink creates a share link row for captureID, after verifying
// captureID belongs to workspaceID.
func (s *Store) CreateShareLink(ctx context.Context, id, captureID, workspaceID, createdBy, token string, expiresAt pgtype.Timestamptz) (sqlcgen.ShareLink, error) {
	if _, err := s.GetCapture(ctx, captureID, workspaceID); err != nil {
		return sqlcgen.ShareLink{}, err
	}
	sl, err := s.q.CreateShareLink(ctx, sqlcgen.CreateShareLinkParams{
		ID: id, CaptureID: captureID, Token: token, CreatedBy: createdBy, ExpiresAt: expiresAt,
	})
	return sl, wrapNotFound(err)
}

// RevokeShareLink marks the share link with id revoked, after verifying it
// belongs to a capture in workspaceID.
func (s *Store) RevokeShareLink(ctx context.Context, id, workspaceID string) (sqlcgen.ShareLink, error) {
	sl, err := s.q.GetShareLink(ctx, id)
	if err != nil {
		return sqlcgen.ShareLink{}, wrapNotFound(err)
	}
	if _, err := s.GetCapture(ctx, sl.CaptureID, workspaceID); err != nil {
		return sqlcgen.ShareLink{}, err
	}
	sl, err = s.q.RevokeShareLink(ctx, id)
	return sl, wrapNotFound(err)
}

// GetShareLink returns the share link row with id, or ErrNotFound. Used by
// the (unauthenticated) share-link resolver — no workspace scoping, since
// the whole point of a share link is access without a workspace
// membership.
func (s *Store) GetShareLink(ctx context.Context, id string) (sqlcgen.ShareLink, error) {
	sl, err := s.q.GetShareLink(ctx, id)
	return sl, wrapNotFound(err)
}

// CreateAuditLog records an audit_log row.
func (s *Store) CreateAuditLog(ctx context.Context, id, workspaceID, actorID, action, subject string, details []byte) (sqlcgen.AuditLog, error) {
	al, err := s.q.CreateAuditLog(ctx, sqlcgen.CreateAuditLogParams{
		ID:          id,
		WorkspaceID: workspaceID,
		ActorID:     pgtype.Text{String: actorID, Valid: actorID != ""},
		Action:      action,
		Subject:     subject,
		Details:     details,
	})
	return al, wrapNotFound(err)
}

// CreateCaptureAndAudit creates a capture and writes the "capture.create"
// audit_log entry in the same transaction, so a capture can never exist
// unaudited. There is no non-transactional CreateCapture wrapper in this
// package on purpose: every write path that creates a capture must go
// through this one, atomic, audited primitive.
func CreateCaptureAndAudit(ctx context.Context, pool db.Pool, actorID string, arg sqlcgen.CreateCaptureParams) (sqlcgen.Capture, error) {
	var c sqlcgen.Capture
	err := db.WithTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		s := New(sqlcgen.New(tx))
		var err error
		c, err = s.q.CreateCapture(ctx, arg)
		if err != nil {
			return wrapNotFound(err)
		}
		_, err = s.CreateAuditLog(ctx, "audit_"+arg.ID, arg.WorkspaceID, actorID, "capture.create", arg.ID, []byte("{}"))
		return err
	})
	return c, err
}

// GetCaptureAndAudit reads a capture (workspace-scoped, same
// no-existence-leak shape as GetCapture) and, on a successful read,
// records exactly one "capture.access" audit_log entry — same
// read-then-audit convention as the share-link resolver (share.go's
// resolve): a plain SELECT has nothing else to roll back if the audit
// write fails, so unlike the purge state machine's writes there is no
// separate Postgres transaction here to fail atomically alongside, just a
// 500 if the audit insert itself errors. auditID is caller-minted
// (capture-api's newID), same convention as every other server-minted row
// this package writes (CreateComment, etc).
func (s *Store) GetCaptureAndAudit(ctx context.Context, id, workspaceID, actorID, auditID string) (sqlcgen.Capture, error) {
	c, err := s.GetCapture(ctx, id, workspaceID)
	if err != nil {
		return sqlcgen.Capture{}, err
	}
	if _, err := s.CreateAuditLog(ctx, auditID, workspaceID, actorID, "capture.access", id, []byte("{}")); err != nil {
		return sqlcgen.Capture{}, err
	}
	return c, nil
}

// TombstoneCaptureForPurge is the purge state machine's pending ->
// tombstoned step (services/purge-job/internal/purge): it flips the
// capture to 'expired' and the purge_jobs row to 'tombstoned', and writes
// the "capture.delete.tombstoned" audit entry, all in one transaction.
// This is the step the brief calls "everything user-visible is finished
// here": the moment this commits, the capture is unreadable, unexportable,
// and unshareable via the same state !== 'ready' gate every read path
// already routes through — whatever happens to its blobs afterward is
// reclamation, not a disclosure risk.
func TombstoneCaptureForPurge(ctx context.Context, pool db.Pool, jobID, captureID, workspaceID string) (sqlcgen.PurgeJob, error) {
	var job sqlcgen.PurgeJob
	err := db.WithTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlcgen.New(tx)
		if _, err := q.TombstoneCaptureForPurge(ctx, captureID); err != nil {
			return fmt.Errorf("store: tombstone capture %s: %w", captureID, err)
		}
		var err error
		job, err = q.SetPurgeJobState(ctx, sqlcgen.SetPurgeJobStateParams{ID: jobID, State: "tombstoned"})
		if err != nil {
			return fmt.Errorf("store: advance purge job %s to tombstoned: %w", jobID, err)
		}
		_, err = q.CreateAuditLog(ctx, sqlcgen.CreateAuditLogParams{
			ID:          "audit_purge_tombstone_" + jobID,
			WorkspaceID: workspaceID,
			Action:      "capture.delete.tombstoned",
			Subject:     captureID,
			Details:     []byte("{}"),
		})
		return err
	})
	return job, err
}

// FinalizePurgeForCapture is the purge state machine's blobs-deleted ->
// purged step: it removes the now-orphaned asset rows, clears the
// capture's disclosable content columns (doc/metadata/env — see
// ClearCaptureContentForPurge's doc comment for why the captures row
// itself cannot be deleted outright), flips the purge_jobs row to
// 'purged', and writes the "capture.delete.purged" audit entry — all in
// one transaction.
func FinalizePurgeForCapture(ctx context.Context, pool db.Pool, jobID, captureID, workspaceID string) (sqlcgen.PurgeJob, error) {
	var job sqlcgen.PurgeJob
	err := db.WithTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlcgen.New(tx)
		// DeleteAssetsForCapture is an unconditional DELETE with no
		// constraint that can reject it (assets has no append-only
		// trigger and no FK pointing at it) — deleting zero matching rows
		// is success, same as everywhere else :execrows is used in this
		// codebase. The error return exists only for a dead connection,
		// not something this test suite can trigger without breaking the
		// transaction wrapper itself.
		if _, err := q.DeleteAssetsForCapture(ctx, captureID); err != nil {
			return fmt.Errorf("store: delete assets for capture %s: %w", captureID, err)
		}
		if _, err := q.ClearCaptureContentForPurge(ctx, captureID); err != nil {
			return fmt.Errorf("store: clear capture content %s: %w", captureID, err)
		}
		var err error
		job, err = q.SetPurgeJobState(ctx, sqlcgen.SetPurgeJobStateParams{ID: jobID, State: "purged"})
		if err != nil {
			return fmt.Errorf("store: advance purge job %s to purged: %w", jobID, err)
		}
		_, err = q.CreateAuditLog(ctx, sqlcgen.CreateAuditLogParams{
			ID:          "audit_purge_final_" + jobID,
			WorkspaceID: workspaceID,
			Action:      "capture.delete.purged",
			Subject:     captureID,
			Details:     []byte("{}"),
		})
		return err
	})
	return job, err
}

// PurgeTxStore adapts the pool-level TombstoneCaptureForPurge/
// FinalizePurgeForCapture functions above to purge.TxStore's
// no-pool-parameter method shape, so the purge pipeline can depend on an
// interface instead of a concrete *pgxpool.Pool.
type PurgeTxStore struct {
	Pool db.Pool
}

// TombstoneCaptureForPurge implements purge.TxStore.
func (s PurgeTxStore) TombstoneCaptureForPurge(ctx context.Context, jobID, captureID, workspaceID string) (sqlcgen.PurgeJob, error) {
	return TombstoneCaptureForPurge(ctx, s.Pool, jobID, captureID, workspaceID)
}

// FinalizePurgeForCapture implements purge.TxStore.
func (s PurgeTxStore) FinalizePurgeForCapture(ctx context.Context, jobID, captureID, workspaceID string) (sqlcgen.PurgeJob, error) {
	return FinalizePurgeForCapture(ctx, s.Pool, jobID, captureID, workspaceID)
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
