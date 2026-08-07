package audit

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/dhiazfathra/how-to-replicate/services/internal/db/sqlcgen"
	"github.com/dhiazfathra/how-to-replicate/services/redaction-audit/internal/redact"
)

// Service is the redaction-audit alarm. It is NOT a redaction step: it
// never writes to captures, capture_events, or assets — see
// service_readonly_test.go. Its only writes are a new
// redaction_audit_findings row and a new audit_log row per finding, plus
// a call to Sink.Alert.
type Service struct {
	store Store
	sink  Sink
}

// New builds a Service. sink is where findings get paged to Security.
func New(store Store, sink Sink) *Service {
	return &Service{store: store, sink: sink}
}

// RunWorkspace re-evaluates every "ready" capture in workspaceID against an
// evaluation ruleset and returns the findings raised. evaluationVersion
// selects which ruleset version to evaluate with: 0 means "use the latest
// ruleset on record for this workspace" (the normal poll-loop case);
// a specific version reproduces a past finding exactly, since a finding is
// fully determined by (capture's events, evaluation ruleset version).
func (s *Service) RunWorkspace(ctx context.Context, workspaceID string, evaluationVersion int32) ([]Finding, error) {
	evalRuleset, err := s.loadEvaluationRuleset(ctx, workspaceID, evaluationVersion)
	if err != nil {
		return nil, err
	}

	ruleset, err := redact.ParseRuleset(evalRuleset.Rules)
	if err != nil {
		return nil, fmt.Errorf("audit: parse evaluation ruleset %d: %w", evalRuleset.Version, err)
	}
	engine, err := redact.New(ruleset)
	if err != nil {
		return nil, fmt.Errorf("audit: build engine for ruleset %d: %w", evalRuleset.Version, err)
	}

	captures, err := s.store.ListReadyCapturesByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("audit: list ready captures: %w", err)
	}

	var findings []Finding
	for _, capture := range captures {
		finding, matched, err := s.evaluateCapture(ctx, engine, evalRuleset.Version, workspaceID, capture)
		if err != nil {
			return findings, err
		}
		if !matched {
			continue
		}
		findings = append(findings, finding)
	}
	return findings, nil
}

func (s *Service) loadEvaluationRuleset(ctx context.Context, workspaceID string, version int32) (sqlcgen.RedactionRuleset, error) {
	if version == 0 {
		rs, err := s.store.GetLatestRedactionRuleset(ctx, workspaceID)
		if err != nil {
			return sqlcgen.RedactionRuleset{}, fmt.Errorf("audit: load latest ruleset: %w", err)
		}
		return rs, nil
	}
	rs, err := s.store.GetRedactionRuleset(ctx, version)
	if err != nil {
		return sqlcgen.RedactionRuleset{}, fmt.Errorf("audit: load ruleset %d: %w", version, err)
	}
	return rs, nil
}

// evaluateCapture re-runs engine over every event of capture. It never
// mutates capture or the events it reads. On a leak, it writes a finding
// row, an audit_log row, and alerts the sink — exactly once per capture per
// run, aggregating every matching rule/event into a single Finding rather
// than one alert per event, so a capture with ten leaking events doesn't
// page Security ten times for the same incident.
func (s *Service) evaluateCapture(
	ctx context.Context,
	engine *redact.Engine,
	evaluationVersion int32,
	workspaceID string,
	capture sqlcgen.Capture,
) (Finding, bool, error) {
	events, err := s.store.ListCaptureEventsByCapture(ctx, capture.ID)
	if err != nil {
		return Finding{}, false, fmt.Errorf("audit: list events for capture %s: %w", capture.ID, err)
	}

	ruleIDSet := map[string]struct{}{}
	var eventIDs []string
	for _, event := range events {
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			// A payload that isn't valid JSON can't be scanned; it also
			// can't contain a JSON-shaped leak, so this is a skip, not a
			// failure of the run.
			continue
		}
		matched := engine.Detect(redact.Event{Kind: event.Kind, Payload: payload})
		if len(matched) == 0 {
			continue
		}
		eventIDs = append(eventIDs, event.ID)
		for _, id := range matched {
			ruleIDSet[id] = struct{}{}
		}
	}

	if len(eventIDs) == 0 {
		return Finding{}, false, nil
	}

	ruleIDs := make([]string, 0, len(ruleIDSet))
	for id := range ruleIDSet {
		ruleIDs = append(ruleIDs, id)
	}
	sort.Strings(ruleIDs)

	var applied *int32
	if capture.AppliedRulesetVersion.Valid {
		v := capture.AppliedRulesetVersion.Int32
		applied = &v
	}

	finding := Finding{
		CaptureID:                capture.ID,
		WorkspaceID:              workspaceID,
		AppliedRulesetVersion:    applied,
		EvaluationRulesetVersion: evaluationVersion,
		RuleIDs:                  ruleIDs,
		EventIDs:                 eventIDs,
	}

	if err := s.recordFinding(ctx, finding); err != nil {
		return Finding{}, false, err
	}
	if err := s.sink.Alert(ctx, finding); err != nil {
		return Finding{}, false, fmt.Errorf("audit: alert sink for capture %s: %w", capture.ID, err)
	}

	return finding, true, nil
}

func (s *Service) recordFinding(ctx context.Context, finding Finding) error {
	// RuleIDs/EventIDs are plain []string and Finding below is a plain
	// struct of strings/slices/*int32 — none of these can fail to marshal.
	ruleIDsJSON, _ := json.Marshal(finding.RuleIDs)
	eventIDsJSON, _ := json.Marshal(finding.EventIDs)

	var appliedVersion pgtype.Int4
	if finding.AppliedRulesetVersion != nil {
		appliedVersion = pgtype.Int4{Int32: *finding.AppliedRulesetVersion, Valid: true}
	}

	if _, err := s.store.CreateRedactionAuditFinding(ctx, sqlcgen.CreateRedactionAuditFindingParams{
		ID:                       newID(),
		CaptureID:                finding.CaptureID,
		WorkspaceID:              finding.WorkspaceID,
		AppliedRulesetVersion:    appliedVersion,
		EvaluationRulesetVersion: finding.EvaluationRulesetVersion,
		RuleIds:                  ruleIDsJSON,
		EventIds:                 eventIDsJSON,
	}); err != nil {
		return fmt.Errorf("audit: write finding for capture %s: %w", finding.CaptureID, err)
	}

	details, _ := json.Marshal(finding)
	if _, err := s.store.CreateAuditLog(ctx, sqlcgen.CreateAuditLogParams{
		ID:          newID(),
		WorkspaceID: finding.WorkspaceID,
		Action:      "redaction_audit.finding",
		Subject:     finding.CaptureID,
		Details:     details,
	}); err != nil {
		return fmt.Errorf("audit: write audit_log for capture %s: %w", finding.CaptureID, err)
	}

	return nil
}

// newID mints a server-side opaque identifier for finding/audit_log rows —
// same convention as capture-api's router.newID: these rows have no client
// of record, the audit service is the only writer.
func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base32.HexEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
}
