// Package policy resolves a workspace's effective policy by layering a
// partial JSON override (stored in workspaces.policy_overrides) over
// hardcoded defaults. Pure functions only — no I/O.
package policy

// Policy is the effective, fully-resolved set of workspace-level caps and
// settings a workspace operates under.
type Policy struct {
	StorageCapBytes  int64    `json:"storageCapBytes"`
	CaptureCap       int      `json:"captureCap"`
	LLMProviderChain []string `json:"llmProviderChain"`
	LocalOnly        bool     `json:"localOnly"`
	OriginAllowList  []string `json:"originAllowList"`
	RetentionDays    int      `json:"retentionDays"`
}

// Defaults are the out-of-the-box values every workspace gets absent an
// override.
//
//   - StorageCapBytes / CaptureCap mirror
//     packages/capture-core/src/storage/budget.ts's DEFAULT_BYTE_LIMIT
//     (2 GB) and DEFAULT_CAPTURE_CAP (40), so client and server agree on
//     the same numbers without either importing the other.
//   - LocalOnly defaults true: privacy-by-default, matching this repo's
//     posture (see CLAUDE.md invariants on redaction/PHI) — a workspace
//     must opt in to any network egress for LLM steps.
//   - LLMProviderChain defaults empty: no provider is called until a
//     workspace names one.
//   - OriginAllowList defaults empty, meaning "same-origin only" — the
//     most restrictive interpretation absent an explicit allow-list.
//   - RetentionDays defaults to 90.
var Defaults = Policy{
	StorageCapBytes:  2 * 1024 * 1024 * 1024,
	CaptureCap:       40,
	LLMProviderChain: []string{},
	LocalOnly:        true,
	OriginAllowList:  []string{},
	RetentionDays:    90,
}

// Overrides is a partial override of Policy, as stored in
// workspaces.policy_overrides. Pointer fields (and nil slices) distinguish
// "not set, use default" from "set to the zero value".
type Overrides struct {
	StorageCapBytes  *int64   `json:"storageCapBytes,omitempty"`
	CaptureCap       *int     `json:"captureCap,omitempty"`
	LLMProviderChain []string `json:"llmProviderChain,omitempty"`
	LocalOnly        *bool    `json:"localOnly,omitempty"`
	OriginAllowList  []string `json:"originAllowList,omitempty"`
	RetentionDays    *int     `json:"retentionDays,omitempty"`
}

// Resolve layers o's non-nil fields onto defaults, returning the effective
// policy. A zero-value Overrides resolves to defaults unchanged.
func Resolve(defaults Policy, o Overrides) Policy {
	p := defaults
	if o.StorageCapBytes != nil {
		p.StorageCapBytes = *o.StorageCapBytes
	}
	if o.CaptureCap != nil {
		p.CaptureCap = *o.CaptureCap
	}
	if o.LLMProviderChain != nil {
		p.LLMProviderChain = o.LLMProviderChain
	}
	if o.LocalOnly != nil {
		p.LocalOnly = *o.LocalOnly
	}
	if o.OriginAllowList != nil {
		p.OriginAllowList = o.OriginAllowList
	}
	if o.RetentionDays != nil {
		p.RetentionDays = *o.RetentionDays
	}
	return p
}
