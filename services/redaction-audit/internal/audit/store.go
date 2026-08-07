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

	"github.com/dhiazfathra/how-to-replicate/services/internal/db/sqlcgen"
)

// Store is the narrow slice of sqlcgen.Querier the audit needs: workspace
// enumeration, ready captures, their events, the ruleset that produced
// applied_ruleset_version, the current evaluation ruleset, and writing a
// finding. Every method here reads capture data or writes exactly one new
// row (redaction_audit_findings / audit_log) — nothing here can update or
// delete a capture, an event, or a ruleset.
type Store interface {
	ListWorkspaces(ctx context.Context) ([]sqlcgen.Workspace, error)
	ListReadyCapturesByWorkspace(ctx context.Context, workspaceID string) ([]sqlcgen.Capture, error)
	ListCaptureEventsByCapture(ctx context.Context, captureID string) ([]sqlcgen.CaptureEvent, error)
	GetRedactionRuleset(ctx context.Context, version int32) (sqlcgen.RedactionRuleset, error)
	GetLatestRedactionRuleset(ctx context.Context, workspaceID string) (sqlcgen.RedactionRuleset, error)
	CreateRedactionAuditFinding(ctx context.Context, arg sqlcgen.CreateRedactionAuditFindingParams) (sqlcgen.RedactionAuditFinding, error)
	CreateAuditLog(ctx context.Context, arg sqlcgen.CreateAuditLogParams) (sqlcgen.AuditLog, error)
}

// compile-time check that the real generated querier satisfies Store.
var _ Store = (sqlcgen.Querier)(nil)
