package redact

// PHIPatterns is a byte-for-byte port of packages/capture-core/src/redaction/
// patterns.ts's PHI_PATTERNS: same ids, same regex source, same flags, same
// order. Keep the two lists in sync manually — conformance_test.go proves
// they agree on every golden-corpus fixture, so any drift here that changes
// detection behavior fails CI immediately.
var PHIPatterns = []Rule{
	{
		ID:      "builtin:phone-id",
		Class:   ClassPattern,
		Pattern: `(?<!\d)(?:\+?62|0)8\d{7,11}\b`,
		Label:   "phone-id",
	},
	{ID: "builtin:nik", Class: ClassPattern, Pattern: `\b\d{16}\b`, Label: "nik"},
	{ID: "builtin:bpjs", Class: ClassPattern, Pattern: `\b\d{13}\b`, Label: "bpjs"},
	{ID: "builtin:mrn", Class: ClassPattern, Pattern: `\bMRN[-\s]?\d{4,10}\b`, Flags: "i", Label: "mrn"},
	{
		ID:      "builtin:email",
		Class:   ClassPattern,
		Pattern: `\b[\w.+-]+@[\w-]+\.[A-Za-z]{2,}\b`,
		Label:   "email",
	},
	{
		ID:      "builtin:dob",
		Class:   ClassPattern,
		Pattern: `\b(?:\d{4}-\d{2}-\d{2}|\d{2}/\d{2}/\d{4})\b`,
		Label:   "dob",
	},
}
