package redact

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dlclark/regexp2"
)

// Event is the subset of a capture_events row the audit needs to evaluate:
// its kind and its JSON payload, decoded generically (map[string]any /
// []any / scalars) rather than into typed structs — the audit reads
// whatever shape is on record, the same way it reads whatever ruleset
// version is on record.
type Event struct {
	Kind    string
	Payload map[string]any
}

// Engine evaluates events against a Ruleset. Detect is read-only: it never
// mutates the Event or Payload passed in, and it never produces a redacted
// value. It only reports which rule ids would have matched — the alarm's
// entire output is that list (empty means "ruleset found nothing to flag").
type Engine struct {
	ruleset  Ruleset
	patterns map[string]*regexp2.Regexp // rule id -> compiled pattern, for ClassPattern rules only
}

// New builds an Engine bound to ruleset. Pattern regexes are compiled once
// up front — both so repeated Detect calls (one per synced event) don't
// recompile them, and so a malformed pattern in the ruleset bundle fails
// loudly at construction instead of silently matching nothing later.
func New(ruleset Ruleset) (*Engine, error) {
	patterns := make(map[string]*regexp2.Regexp, len(ruleset.Rules))
	for _, rule := range ruleset.Rules {
		if rule.Class != ClassPattern {
			continue
		}
		re, err := compilePattern(rule.Pattern, rule.Flags)
		if err != nil {
			return nil, fmt.Errorf("redact: rule %q: %w", rule.ID, err)
		}
		patterns[rule.ID] = re
	}
	return &Engine{ruleset: ruleset, patterns: patterns}, nil
}

// Version returns the ruleset version this Engine evaluates with — the
// "evaluation_ruleset_version" half of a finding's version pair.
func (e *Engine) Version() string { return e.ruleset.Version }

// Detect returns the ids of every rule that matches something in event,
// mirroring the TS engine's per-rule `applied` bookkeeping (engine.ts).
// video-blur and origin-allow rules never match an event payload — same as
// the TS engine, where they're surfaced via separate accessors instead.
func (e *Engine) Detect(event Event) []string {
	var applied []string
	for _, rule := range e.ruleset.Rules {
		if e.ruleMatches(rule, event.Kind, event.Payload) {
			applied = append(applied, rule.ID)
		}
	}
	return applied
}

func (e *Engine) ruleMatches(rule Rule, kind string, payload map[string]any) bool {
	switch rule.Class {
	case ClassFieldPath:
		return fieldPathMatches(rule.Pointer, kind, payload)
	case ClassHeader:
		return headerMatches(rule.Name, kind, payload)
	case ClassPattern:
		return patternMatches(e.patterns[rule.ID], kind, payload)
	case ClassDomSelector:
		return domSelectorMatches(rule.Selector, kind, payload)
	case ClassVideoBlur, ClassOriginAllow:
		return false
	default:
		return false
	}
}

// --- field-path --------------------------------------------------------

func fieldPathMatches(pointer, kind string, payload map[string]any) bool {
	if kind != "network" {
		return false
	}
	segments := parsePointer(pointer)
	for _, key := range []string{"requestBody", "responseBody"} {
		raw, ok := payload[key].(string)
		if !ok || raw == "" {
			continue // absent, non-string, or JSON null decoded away — TS treats null the same: skip.
		}
		var parsed any
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			continue // not JSON — field-path does not apply, same as engine.ts.
		}
		if pointerExists(parsed, segments) {
			return true
		}
	}
	return false
}

func parsePointer(pointer string) []string {
	if pointer == "" || pointer == "/" {
		return nil
	}
	raw := strings.TrimPrefix(pointer, "/")
	parts := strings.Split(raw, "/")
	for i, p := range parts {
		p = strings.ReplaceAll(p, "~1", "/")
		p = strings.ReplaceAll(p, "~0", "~")
		parts[i] = p
	}
	return parts
}

// pointerExists reports whether segments resolves to an existing location
// inside value, mirroring engine.ts's redactAtPointer traversal (root,
// wildcard, array index, object key). Detection only needs existence, not
// the redacted value engine.ts would also compute.
//
// ponytail: a wildcard segment over a non-container value is a malformed
// rule in engine.ts (it throws, and the whole event is dropped client-side —
// meaning it never reaches audit at all, since a dropped event never syncs).
// Here it's treated as "no match" instead of an error, which only under-
// detects a case that cannot occur in real synced data. Revisit if audit
// ever evaluates ruleset changes against not-yet-synced data.
func pointerExists(value any, segments []string) bool {
	if len(segments) == 0 {
		return true
	}
	segment, rest := segments[0], segments[1:]

	if segment == "*" {
		switch v := value.(type) {
		case []any:
			for _, item := range v {
				if pointerExists(item, rest) {
					return true
				}
			}
			return false
		case map[string]any:
			for _, item := range v {
				if pointerExists(item, rest) {
					return true
				}
			}
			return false
		default:
			return false
		}
	}

	switch v := value.(type) {
	case []any:
		index, ok := parseArrayIndex(segment)
		if !ok || index >= len(v) {
			return false
		}
		return pointerExists(v[index], rest)
	case map[string]any:
		child, ok := v[segment]
		if !ok {
			return false
		}
		return pointerExists(child, rest)
	default:
		return false
	}
}

func parseArrayIndex(segment string) (int, bool) {
	if segment == "" {
		return 0, false
	}
	n := 0
	for _, c := range segment {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// --- header --------------------------------------------------------------

func headerMatches(name, kind string, payload map[string]any) bool {
	if kind != "network" {
		return false
	}
	lower := strings.ToLower(name)
	for _, key := range []string{"requestHeaders", "responseHeaders"} {
		headers, ok := payload[key].(map[string]any)
		if !ok {
			continue
		}
		for k := range headers {
			if strings.ToLower(k) == lower {
				return true
			}
		}
	}
	return false
}

// --- dom-selector ----------------------------------------------------------

func domSelectorMatches(selector, kind string, payload map[string]any) bool {
	if kind != "interaction" {
		return false
	}
	target, ok := payload["targetSelector"].(string)
	return ok && target == selector
}

// --- pattern ---------------------------------------------------------------

func compilePattern(pattern, flags string) (*regexp2.Regexp, error) {
	opts := regexp2.None
	if strings.Contains(flags, "i") {
		opts |= regexp2.IgnoreCase
	}
	re, err := regexp2.Compile(pattern, opts)
	if err != nil {
		return nil, fmt.Errorf("compile pattern %q: %w", pattern, err)
	}
	return re, nil
}

func regexMatchesString(re *regexp2.Regexp, s string) bool {
	m, err := re.MatchString(s)
	return err == nil && m
}

func patternMatches(re *regexp2.Regexp, kind string, payload map[string]any) bool {
	if kind == "network" {
		for _, key := range []string{"requestBody", "responseBody"} {
			raw, ok := payload[key].(string)
			if !ok {
				continue
			}
			var parsed any
			if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
				// Freeform JSON content: keys AND values are in scope, same
				// as engine.ts's deepPatternWalk.
				if anyStringMatches(parsed, re, true) {
					return true
				}
			} else if regexMatchesString(re, raw) {
				return true
			}
		}
		for key, value := range payload {
			if key == "requestBody" || key == "responseBody" {
				continue
			}
			// The payload's own schema fields: values only, never key
			// names, same as engine.ts's patternMapValues.
			if anyStringMatches(value, re, false) {
				return true
			}
		}
		return false
	}

	return anyStringMatches(payload, re, false)
}

// anyStringMatches walks value looking for any string re matches. When
// includeKeys is true, map keys are checked too (freeform JSON content);
// otherwise only values are (a payload's own schema fields).
func anyStringMatches(value any, re *regexp2.Regexp, includeKeys bool) bool {
	switch v := value.(type) {
	case string:
		return regexMatchesString(re, v)
	case []any:
		for _, item := range v {
			if anyStringMatches(item, re, includeKeys) {
				return true
			}
		}
		return false
	case map[string]any:
		for k, val := range v {
			if includeKeys && regexMatchesString(re, k) {
				return true
			}
			if anyStringMatches(val, re, includeKeys) {
				return true
			}
		}
		return false
	default:
		return false
	}
}
