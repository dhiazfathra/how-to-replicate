package redact

import (
	"encoding/json"
	"os"
	"sort"
	"testing"
)

// corpusFile mirrors the JSON shape written by
// packages/capture-core/scripts/export-audit-corpus.ts — the SAME golden
// fixtures Phase 0's redaction-corpus.test.ts runs against the TS engine,
// exported (not hand-copied) so this suite proves the Go port agrees with
// the TS source of truth on every fixture, not on a maintainer's memory of
// it.
type corpusFile struct {
	RulesetVersion string          `json:"rulesetVersion"`
	Ruleset        json.RawMessage `json:"ruleset"`
	Fixtures       []corpusFixture `json:"fixtures"`
}

type corpusFixture struct {
	ID             string         `json:"id"`
	Kind           string         `json:"kind"`
	Payload        map[string]any `json:"payload"`
	Forbidden      []string       `json:"forbidden"`
	TSFidelity     string         `json:"tsFidelity"`
	TSRulesApplied []string       `json:"tsRulesApplied"`
}

func loadCorpus(t *testing.T) corpusFile {
	t.Helper()
	data, err := os.ReadFile("../../testdata/corpus.json")
	if err != nil {
		t.Fatalf("read corpus fixture (run `pnpm --filter @htr/capture-core export:audit-corpus` after any redaction change): %v", err)
	}
	var cf corpusFile
	if err := json.Unmarshal(data, &cf); err != nil {
		t.Fatalf("parse corpus fixture: %v", err)
	}
	return cf
}

func sortedCopy(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

// TestConformance_MatchesTSEngine proves the Go audit engine agrees with the
// TS redaction engine on which rules fire for every fixture in the shared
// corpus. A fixture the TS engine flags but Go misses (or vice versa) is
// exactly the divergence this task exists to prevent — see task-12-brief.md.
func TestConformance_MatchesTSEngine(t *testing.T) {
	cf := loadCorpus(t)
	if len(cf.Fixtures) == 0 {
		t.Fatal("corpus fixture file is empty — golden corpus must not be trivial")
	}

	ruleset, err := ParseRuleset(cf.Ruleset)
	if err != nil {
		t.Fatalf("parse ruleset from corpus: %v", err)
	}
	if ruleset.Version != cf.RulesetVersion {
		t.Fatalf("ruleset version mismatch: file says %q, parsed ruleset says %q", cf.RulesetVersion, ruleset.Version)
	}

	engine, err := New(ruleset)
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}

	for _, fixture := range cf.Fixtures {
		fixture := fixture
		t.Run(fixture.ID, func(t *testing.T) {
			got := sortedCopy(engine.Detect(Event{Kind: fixture.Kind, Payload: fixture.Payload}))
			want := sortedCopy(fixture.TSRulesApplied)

			if len(got) != len(want) {
				t.Fatalf("rule set mismatch: go detected %v, ts detected %v", got, want)
			}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("rule set mismatch: go detected %v, ts detected %v", got, want)
				}
			}
		})
	}
}
