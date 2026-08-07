package audit

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/dhiazfathra/how-to-replicate/services/internal/db/sqlcgen"
)

// fakeStore is an in-memory Store used by service_test.go and
// service_readonly_test.go. It never mutates a Capture or CaptureEvent
// value it holds — CreateRedactionAuditFinding/CreateAuditLog only ever
// append to their own slices — mirroring the real schema's append-only
// findings/audit_log tables and read-only-to-the-service capture tables.
type fakeStore struct {
	workspaces []sqlcgen.Workspace
	captures   map[string][]sqlcgen.Capture // workspaceID -> captures
	events     map[string][]sqlcgen.CaptureEvent
	rulesets   map[int32]sqlcgen.RedactionRuleset
	latestByWS map[string]int32

	findings  []sqlcgen.RedactionAuditFinding
	auditLogs []sqlcgen.AuditLog

	errListWorkspaces error
	errListCaptures   error
	errListEvents     error
	errGetRuleset     error
	errGetLatest      error
	errCreateFinding  error
	errCreateAudit    error
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		captures:   map[string][]sqlcgen.Capture{},
		events:     map[string][]sqlcgen.CaptureEvent{},
		rulesets:   map[int32]sqlcgen.RedactionRuleset{},
		latestByWS: map[string]int32{},
	}
}

func (f *fakeStore) ListWorkspaces(ctx context.Context) ([]sqlcgen.Workspace, error) {
	return f.workspaces, f.errListWorkspaces
}

func (f *fakeStore) ListReadyCapturesByWorkspace(ctx context.Context, workspaceID string) ([]sqlcgen.Capture, error) {
	if f.errListCaptures != nil {
		return nil, f.errListCaptures
	}
	return f.captures[workspaceID], nil
}

func (f *fakeStore) ListCaptureEventsByCapture(ctx context.Context, captureID string) ([]sqlcgen.CaptureEvent, error) {
	if f.errListEvents != nil {
		return nil, f.errListEvents
	}
	return f.events[captureID], nil
}

func (f *fakeStore) GetRedactionRuleset(ctx context.Context, version int32) (sqlcgen.RedactionRuleset, error) {
	if f.errGetRuleset != nil {
		return sqlcgen.RedactionRuleset{}, f.errGetRuleset
	}
	rs, ok := f.rulesets[version]
	if !ok {
		return sqlcgen.RedactionRuleset{}, errors.New("fakestore: ruleset not found")
	}
	return rs, nil
}

func (f *fakeStore) GetLatestRedactionRuleset(ctx context.Context, workspaceID string) (sqlcgen.RedactionRuleset, error) {
	if f.errGetLatest != nil {
		return sqlcgen.RedactionRuleset{}, f.errGetLatest
	}
	version, ok := f.latestByWS[workspaceID]
	if !ok {
		return sqlcgen.RedactionRuleset{}, errors.New("fakestore: no ruleset for workspace")
	}
	return f.rulesets[version], nil
}

func (f *fakeStore) CreateRedactionAuditFinding(ctx context.Context, arg sqlcgen.CreateRedactionAuditFindingParams) (sqlcgen.RedactionAuditFinding, error) {
	if f.errCreateFinding != nil {
		return sqlcgen.RedactionAuditFinding{}, f.errCreateFinding
	}
	row := sqlcgen.RedactionAuditFinding{
		ID:                       arg.ID,
		CaptureID:                arg.CaptureID,
		WorkspaceID:              arg.WorkspaceID,
		AppliedRulesetVersion:    arg.AppliedRulesetVersion,
		EvaluationRulesetVersion: arg.EvaluationRulesetVersion,
		RuleIds:                  arg.RuleIds,
		EventIds:                 arg.EventIds,
	}
	f.findings = append(f.findings, row)
	return row, nil
}

func (f *fakeStore) CreateAuditLog(ctx context.Context, arg sqlcgen.CreateAuditLogParams) (sqlcgen.AuditLog, error) {
	if f.errCreateAudit != nil {
		return sqlcgen.AuditLog{}, f.errCreateAudit
	}
	row := sqlcgen.AuditLog{
		ID:          arg.ID,
		WorkspaceID: arg.WorkspaceID,
		ActorID:     arg.ActorID,
		Action:      arg.Action,
		Subject:     arg.Subject,
		Details:     arg.Details,
	}
	f.auditLogs = append(f.auditLogs, row)
	return row, nil
}

// fakeSink records every Finding it's asked to Alert on. Set err to make
// Alert fail.
type fakeSink struct {
	findings []Finding
	err      error
}

func (s *fakeSink) Alert(ctx context.Context, finding Finding) error {
	if s.err != nil {
		return s.err
	}
	s.findings = append(s.findings, finding)
	return nil
}

func int4(v int32) pgtype.Int4 { return pgtype.Int4{Int32: v, Valid: true} }
