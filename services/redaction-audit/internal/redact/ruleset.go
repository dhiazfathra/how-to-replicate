// Package redact is the server-side half of the redaction-audit alarm
// (spec §11, CLAUDE.md invariant 7). It is NOT a redaction step: nothing in
// this package ever produces a redacted payload for storage or display. It
// only ANSWERS the question "would a rule in this ruleset have matched
// something in this already-synced event?" — the answer feeds an alert, a
// finding row, and an audit_log entry, never a mutation of capture data.
//
// Rule semantics here must agree with the Phase 0 TypeScript engine
// (packages/capture-core/src/redaction) on every fixture in the golden
// corpus — see conformance_test.go, which is driven by the same fixtures as
// packages/capture-core/test/redaction-corpus.test.ts via a JSON export
// (packages/capture-core/scripts/export-audit-corpus.ts), not a hand-copied
// duplicate.
package redact

import (
	"encoding/json"
	"fmt"
)

// RuleClass mirrors packages/capture-core/src/redaction/ruleset.ts's
// RuleClass union.
type RuleClass string

// Rule classes, matching packages/capture-core/src/redaction/ruleset.ts's
// RuleClass union member-for-member.
const (
	ClassFieldPath   RuleClass = "field-path"
	ClassHeader      RuleClass = "header"
	ClassPattern     RuleClass = "pattern"
	ClassDomSelector RuleClass = "dom-selector"
	ClassVideoBlur   RuleClass = "video-blur"
	ClassOriginAllow RuleClass = "origin-allow"
)

// Rule is a single redaction rule. Only the fields relevant to its Class are
// populated, matching the TS discriminated union's shape once decoded from
// the same JSON wire format (the ruleset bundle stored in
// redaction_rulesets.rules).
type Rule struct {
	ID       string    `json:"id"`
	Class    RuleClass `json:"class"`
	Pointer  string    `json:"pointer,omitempty"`
	Name     string    `json:"name,omitempty"`
	Pattern  string    `json:"pattern,omitempty"`
	Flags    string    `json:"flags,omitempty"`
	Label    string    `json:"label,omitempty"`
	Selector string    `json:"selector,omitempty"`
	Origins  []string  `json:"origins,omitempty"`
}

// Ruleset is the same versioned bundle format the client redacts with.
type Ruleset struct {
	Version string `json:"version"`
	Rules   []Rule `json:"rules"`
}

// ParseRuleset decodes a ruleset bundle from JSON (as stored in
// redaction_rulesets.rules, or exported by Phase 0's test corpus). Unlike
// the TS parser, this does not re-validate rule shape/regex-compilability at
// parse time — the bundle was already validated at authoring time (Task 3's
// ruleset store) and audit trusts what is on record, the same object the
// client evaluated against.
func ParseRuleset(data []byte) (Ruleset, error) {
	var rs Ruleset
	if err := json.Unmarshal(data, &rs); err != nil {
		return Ruleset{}, fmt.Errorf("redact: parse ruleset: %w", err)
	}
	if rs.Version == "" {
		return Ruleset{}, fmt.Errorf("redact: ruleset missing version")
	}
	return rs, nil
}
