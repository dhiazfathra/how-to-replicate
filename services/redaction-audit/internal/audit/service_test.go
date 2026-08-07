package audit

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/dhiazfathra/how-to-replicate/services/internal/db/sqlcgen"
)

func rulesetBytes(t *testing.T, version int32, rules ...map[string]any) []byte {
	t.Helper()
	body := map[string]any{"version": strconv.Itoa(int(version)), "rules": rules}
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal ruleset: %v", err)
	}
	return b
}

func nikRule() map[string]any {
	return map[string]any{"id": "builtin:nik", "class": "pattern", "pattern": `\b\d{16}\b`, "label": "nik"}
}

func networkEventPayload(body string) []byte {
	payload := map[string]any{
		"method":          "POST",
		"url":             "https://api.example.com",
		"requestBody":     body,
		"responseBody":    nil,
		"requestHeaders":  map[string]string{},
		"responseHeaders": map[string]string{},
	}
	b, _ := json.Marshal(payload)
	return b
}

func TestRunWorkspace_LeakTriggersExactlyOneAlertWithBothVersions(t *testing.T) {
	store := newFakeStore()
	store.rulesets[2] = sqlcgen.RedactionRuleset{Version: 2, WorkspaceID: "ws1", Rules: rulesetBytes(t, 2, nikRule())}
	store.latestByWS["ws1"] = 2
	store.captures["ws1"] = []sqlcgen.Capture{
		{ID: "cap1", WorkspaceID: "ws1", State: "ready", AppliedRulesetVersion: int4(1)},
	}
	store.events["cap1"] = []sqlcgen.CaptureEvent{
		{ID: "evt1", CaptureID: "cap1", Kind: "network", Payload: networkEventPayload(`{"note":"1234567890123456"}`)},
		{ID: "evt2", CaptureID: "cap1", Kind: "annotation", Payload: []byte(`{"text":"nothing here"}`)},
	}

	sink := &fakeSink{}
	svc := New(store, sink)

	findings, err := svc.RunWorkspace(context.Background(), "ws1", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected exactly 1 finding, got %d", len(findings))
	}
	if len(sink.findings) != 1 {
		t.Fatalf("expected exactly 1 alert, got %d", len(sink.findings))
	}

	f := findings[0]
	if f.AppliedRulesetVersion == nil || *f.AppliedRulesetVersion != 1 {
		t.Fatalf("expected applied ruleset version 1, got %v", f.AppliedRulesetVersion)
	}
	if f.EvaluationRulesetVersion != 2 {
		t.Fatalf("expected evaluation ruleset version 2, got %d", f.EvaluationRulesetVersion)
	}
	if len(f.EventIDs) != 1 || f.EventIDs[0] != "evt1" {
		t.Fatalf("expected only evt1 flagged, got %v", f.EventIDs)
	}
	if len(f.RuleIDs) != 1 || f.RuleIDs[0] != "builtin:nik" {
		t.Fatalf("expected builtin:nik flagged, got %v", f.RuleIDs)
	}

	// Both the finding row and the audit_log row must carry the pair.
	if len(store.findings) != 1 {
		t.Fatalf("expected 1 persisted finding, got %d", len(store.findings))
	}
	persisted := store.findings[0]
	if !persisted.AppliedRulesetVersion.Valid || persisted.AppliedRulesetVersion.Int32 != 1 {
		t.Fatalf("persisted finding missing applied version: %+v", persisted.AppliedRulesetVersion)
	}
	if persisted.EvaluationRulesetVersion != 2 {
		t.Fatalf("persisted finding missing evaluation version: %d", persisted.EvaluationRulesetVersion)
	}
	if len(store.auditLogs) != 1 {
		t.Fatalf("expected 1 audit_log row, got %d", len(store.auditLogs))
	}
	if store.auditLogs[0].Action != "redaction_audit.finding" {
		t.Fatalf("unexpected audit_log action: %s", store.auditLogs[0].Action)
	}
}

func TestRunWorkspace_OlderCaptureClassifiedAgainstPinnedVersion(t *testing.T) {
	store := newFakeStore()
	// Two ruleset versions: v1 has no rules at all (so it wouldn't have
	// flagged this leak), v2 (current/latest) has the nik pattern.
	store.rulesets[1] = sqlcgen.RedactionRuleset{Version: 1, WorkspaceID: "ws1", Rules: rulesetBytes(t, 1)}
	store.rulesets[2] = sqlcgen.RedactionRuleset{Version: 2, WorkspaceID: "ws1", Rules: rulesetBytes(t, 2, nikRule())}
	store.latestByWS["ws1"] = 2
	store.captures["ws1"] = []sqlcgen.Capture{
		{ID: "cap-old", WorkspaceID: "ws1", State: "ready", AppliedRulesetVersion: int4(1)},
	}
	store.events["cap-old"] = []sqlcgen.CaptureEvent{
		{ID: "evt1", CaptureID: "cap-old", Kind: "network", Payload: networkEventPayload(`{"note":"1234567890123456"}`)},
	}

	svc := New(store, &fakeSink{})

	// Pinning evaluationVersion=1 reproduces "this ruleset would not have
	// found anything" — no finding.
	findingsV1, err := svc.RunWorkspace(context.Background(), "ws1", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findingsV1) != 0 {
		t.Fatalf("expected no findings evaluated against v1, got %d", len(findingsV1))
	}

	// The default/latest run (v2) finds it and classifies the finding
	// against v2, not v1 — a genuine gap in the ruleset the client had,
	// not one that existed yet.
	findingsLatest, err := svc.RunWorkspace(context.Background(), "ws1", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findingsLatest) != 1 {
		t.Fatalf("expected 1 finding against latest ruleset, got %d", len(findingsLatest))
	}
	if findingsLatest[0].EvaluationRulesetVersion != 2 {
		t.Fatalf("expected evaluation version 2, got %d", findingsLatest[0].EvaluationRulesetVersion)
	}
	if *findingsLatest[0].AppliedRulesetVersion != 1 {
		t.Fatalf("expected applied version to remain 1, got %d", *findingsLatest[0].AppliedRulesetVersion)
	}

	// Re-running pinned to the SAME pair reproduces the SAME finding.
	findingsReproduced, err := svc.RunWorkspace(context.Background(), "ws1", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findingsReproduced) != 1 {
		t.Fatalf("expected reproduced finding, got %d", len(findingsReproduced))
	}
	if findingsReproduced[0].EventIDs[0] != findingsLatest[0].EventIDs[0] ||
		findingsReproduced[0].RuleIDs[0] != findingsLatest[0].RuleIDs[0] {
		t.Fatalf("reproduced finding does not match original: %+v vs %+v", findingsReproduced[0], findingsLatest[0])
	}
}

func TestRunWorkspace_NoLeakNoFindingNoAlert(t *testing.T) {
	store := newFakeStore()
	store.rulesets[1] = sqlcgen.RedactionRuleset{Version: 1, WorkspaceID: "ws1", Rules: rulesetBytes(t, 1, nikRule())}
	store.latestByWS["ws1"] = 1
	store.captures["ws1"] = []sqlcgen.Capture{{ID: "cap1", WorkspaceID: "ws1", State: "ready"}}
	store.events["cap1"] = []sqlcgen.CaptureEvent{
		{ID: "evt1", CaptureID: "cap1", Kind: "network", Payload: networkEventPayload(`{"note":"[REDACTED:nik]"}`)},
	}

	sink := &fakeSink{}
	svc := New(store, sink)
	findings, err := svc.RunWorkspace(context.Background(), "ws1", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected no findings, got %d", len(findings))
	}
	if len(sink.findings) != 0 {
		t.Fatalf("expected no alerts, got %d", len(sink.findings))
	}
	if len(store.findings) != 0 || len(store.auditLogs) != 0 {
		t.Fatal("expected no rows written when nothing was found")
	}
}

func TestRunWorkspace_NoAppliedRulesetVersionOnCapture(t *testing.T) {
	store := newFakeStore()
	store.rulesets[1] = sqlcgen.RedactionRuleset{Version: 1, WorkspaceID: "ws1", Rules: rulesetBytes(t, 1, nikRule())}
	store.latestByWS["ws1"] = 1
	store.captures["ws1"] = []sqlcgen.Capture{
		{ID: "cap1", WorkspaceID: "ws1", State: "ready", AppliedRulesetVersion: pgtype.Int4{}},
	}
	store.events["cap1"] = []sqlcgen.CaptureEvent{
		{ID: "evt1", CaptureID: "cap1", Kind: "network", Payload: networkEventPayload(`{"note":"1234567890123456"}`)},
	}

	findings, err := New(store, &fakeSink{}).RunWorkspace(context.Background(), "ws1", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].AppliedRulesetVersion != nil {
		t.Fatalf("expected nil applied ruleset version, got %v", *findings[0].AppliedRulesetVersion)
	}
}

func TestRunWorkspace_SkipsUnparseableEventPayload(t *testing.T) {
	store := newFakeStore()
	store.rulesets[1] = sqlcgen.RedactionRuleset{Version: 1, WorkspaceID: "ws1", Rules: rulesetBytes(t, 1, nikRule())}
	store.latestByWS["ws1"] = 1
	store.captures["ws1"] = []sqlcgen.Capture{{ID: "cap1", WorkspaceID: "ws1", State: "ready"}}
	store.events["cap1"] = []sqlcgen.CaptureEvent{
		{ID: "evt1", CaptureID: "cap1", Kind: "network", Payload: []byte(`not json`)},
	}

	findings, err := New(store, &fakeSink{}).RunWorkspace(context.Background(), "ws1", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected no findings, got %d", len(findings))
	}
}

func TestRunWorkspace_ErrorPaths(t *testing.T) {
	baseStore := func() *fakeStore {
		s := newFakeStore()
		s.rulesets[1] = sqlcgen.RedactionRuleset{Version: 1, WorkspaceID: "ws1", Rules: rulesetBytes(t, 1, nikRule())}
		s.latestByWS["ws1"] = 1
		s.captures["ws1"] = []sqlcgen.Capture{{ID: "cap1", WorkspaceID: "ws1", State: "ready"}}
		s.events["cap1"] = []sqlcgen.CaptureEvent{
			{ID: "evt1", CaptureID: "cap1", Kind: "network", Payload: networkEventPayload(`{"note":"1234567890123456"}`)},
		}
		return s
	}

	t.Run("get latest ruleset fails", func(t *testing.T) {
		s := baseStore()
		s.errGetLatest = errors.New("boom")
		if _, err := New(s, &fakeSink{}).RunWorkspace(context.Background(), "ws1", 0); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("get pinned ruleset fails", func(t *testing.T) {
		s := baseStore()
		s.errGetRuleset = errors.New("boom")
		if _, err := New(s, &fakeSink{}).RunWorkspace(context.Background(), "ws1", 1); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("malformed ruleset JSON", func(t *testing.T) {
		s := baseStore()
		s.rulesets[1] = sqlcgen.RedactionRuleset{Version: 1, WorkspaceID: "ws1", Rules: []byte(`not json`)}
		if _, err := New(s, &fakeSink{}).RunWorkspace(context.Background(), "ws1", 0); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("ruleset with invalid pattern fails engine construction", func(t *testing.T) {
		s := baseStore()
		s.rulesets[1] = sqlcgen.RedactionRuleset{Version: 1, WorkspaceID: "ws1", Rules: rulesetBytes(t, 1, map[string]any{
			"id": "bad", "class": "pattern", "pattern": "(unterminated", "label": "bad",
		})}
		if _, err := New(s, &fakeSink{}).RunWorkspace(context.Background(), "ws1", 0); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("list ready captures fails", func(t *testing.T) {
		s := baseStore()
		s.errListCaptures = errors.New("boom")
		if _, err := New(s, &fakeSink{}).RunWorkspace(context.Background(), "ws1", 0); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("list events fails, returns partial findings and error", func(t *testing.T) {
		s := baseStore()
		s.errListEvents = errors.New("boom")
		findings, err := New(s, &fakeSink{}).RunWorkspace(context.Background(), "ws1", 0)
		if err == nil {
			t.Fatal("expected error")
		}
		if len(findings) != 0 {
			t.Fatalf("expected no findings, got %d", len(findings))
		}
	})

	t.Run("create finding fails", func(t *testing.T) {
		s := baseStore()
		s.errCreateFinding = errors.New("boom")
		if _, err := New(s, &fakeSink{}).RunWorkspace(context.Background(), "ws1", 0); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("create audit log fails", func(t *testing.T) {
		s := baseStore()
		s.errCreateAudit = errors.New("boom")
		if _, err := New(s, &fakeSink{}).RunWorkspace(context.Background(), "ws1", 0); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("alert sink fails", func(t *testing.T) {
		s := baseStore()
		if _, err := New(s, &fakeSink{err: errors.New("paging failed")}).RunWorkspace(context.Background(), "ws1", 0); err == nil {
			t.Fatal("expected error")
		}
	})
}
