// Package audit implements the redaction-audit alarm: it re-runs the
// current redaction ruleset over already-synced captures and, on any
// finding, alerts Security and records the finding. It is read-only
// against capture data — see CLAUDE.md invariant 7 and
// service_readonly_test.go, which proves it with a byte-for-byte
// before/after comparison of every row this package reads, not just an
// absence of update-call sites.
package audit

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/dhiazfathra/how-to-replicate/services/internal/db"
	"github.com/dhiazfathra/how-to-replicate/services/internal/db/sqlcgen"
)

// Store is the narrow slice of sqlcgen.Querier the audit needs: workspace
// enumeration, ready captures, their events, the ruleset that produced
// applied_ruleset_version, the current evaluation ruleset, and writing a
// finding. Every method here reads capture data or writes exactly one new
// row (redaction_audit_findings / audit_log) — nothing here can update or
// delete a capture, an event, or a ruleset.
//
// CreateFindingAndAuditLog writes both the finding row and its audit_log
// row atomically: "an action can never succeed unaudited" means a finding
// that committed without its audit_log row (e.g. the process died between
// two independent inserts) is exactly the failure this method rules out.
// See PoolStore below for the transactional implementation, mirroring
// capture-api's store.CreateWorkspaceWithOwner / TombstoneCaptureForPurge
// pattern of wrapping multiple writes in a single db.WithTx.
type Store interface {
	ListWorkspaces(ctx context.Context) ([]sqlcgen.Workspace, error)
	ListReadyCapturesByWorkspace(ctx context.Context, workspaceID string) ([]sqlcgen.Capture, error)
	ListCaptureEventsByCapture(ctx context.Context, captureID string) ([]sqlcgen.CaptureEvent, error)
	GetRedactionRuleset(ctx context.Context, version int32) (sqlcgen.RedactionRuleset, error)
	GetLatestRedactionRuleset(ctx context.Context, workspaceID string) (sqlcgen.RedactionRuleset, error)
	CreateFindingAndAuditLog(ctx context.Context, finding sqlcgen.CreateRedactionAuditFindingParams, log sqlcgen.CreateAuditLogParams) (sqlcgen.RedactionAuditFinding, error)
}

// PoolStore is the production Store: reads go straight through a
// sqlcgen.Queries bound to the pool, but CreateFindingAndAuditLog runs both
// writes inside a single db.WithTx transaction so they commit or roll back
// together.
type PoolStore struct {
	q    *sqlcgen.Queries
	pool db.Pool
}

// NewPoolStore builds the production Store from a live connection pool.
func NewPoolStore(pool db.Pool) *PoolStore {
	// pool.(sqlcgen.DBTX) — every db.Pool implementation used in production
	// (*pgxpool.Pool) also satisfies sqlcgen.DBTX; this cast is asserted, not
	// checked, matching how every other service constructs its Queries.
	dbtx, _ := pool.(sqlcgen.DBTX)
	return &PoolStore{q: sqlcgen.New(dbtx), pool: pool}
}

// ListWorkspaces delegates straight to the pool-backed querier.
func (p *PoolStore) ListWorkspaces(ctx context.Context) ([]sqlcgen.Workspace, error) {
	return p.q.ListWorkspaces(ctx)
}

// ListReadyCapturesByWorkspace delegates straight to the pool-backed querier.
func (p *PoolStore) ListReadyCapturesByWorkspace(ctx context.Context, workspaceID string) ([]sqlcgen.Capture, error) {
	return p.q.ListReadyCapturesByWorkspace(ctx, workspaceID)
}

// ListCaptureEventsByCapture delegates straight to the pool-backed querier.
func (p *PoolStore) ListCaptureEventsByCapture(ctx context.Context, captureID string) ([]sqlcgen.CaptureEvent, error) {
	return p.q.ListCaptureEventsByCapture(ctx, captureID)
}

// GetRedactionRuleset delegates straight to the pool-backed querier.
func (p *PoolStore) GetRedactionRuleset(ctx context.Context, version int32) (sqlcgen.RedactionRuleset, error) {
	return p.q.GetRedactionRuleset(ctx, version)
}

// GetLatestRedactionRuleset delegates straight to the pool-backed querier.
func (p *PoolStore) GetLatestRedactionRuleset(ctx context.Context, workspaceID string) (sqlcgen.RedactionRuleset, error) {
	return p.q.GetLatestRedactionRuleset(ctx, workspaceID)
}

// CreateFindingAndAuditLog inserts the finding row and its audit_log row in
// one transaction: if the audit_log insert fails (or the process dies)
// after the finding row would have committed, the whole transaction rolls
// back instead, so a finding never exists without its audit trail.
func (p *PoolStore) CreateFindingAndAuditLog(
	ctx context.Context,
	finding sqlcgen.CreateRedactionAuditFindingParams,
	log sqlcgen.CreateAuditLogParams,
) (sqlcgen.RedactionAuditFinding, error) {
	var row sqlcgen.RedactionAuditFinding
	err := db.WithTx(ctx, p.pool, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlcgen.New(tx)
		var err error
		row, err = q.CreateRedactionAuditFinding(ctx, finding)
		if err != nil {
			return fmt.Errorf("audit: write finding for capture %s: %w", finding.CaptureID, err)
		}
		if _, err := q.CreateAuditLog(ctx, log); err != nil {
			return fmt.Errorf("audit: write audit_log for capture %s: %w", finding.CaptureID, err)
		}
		return nil
	})
	return row, err
}
