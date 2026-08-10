# redaction-audit

The server-side redaction **alarm** (spec §11). By the time a capture reaches this
service, its events have already synced to Postgres — any unredacted PHI in them has
already crossed the network boundary. `redaction-audit` cannot undo that. What it does
is periodically re-run the *current* redaction ruleset over every `ready` capture's
events and metadata, and if the ruleset would still flag something, it:

1. writes a `redaction_audit_findings` row (append-only),
2. writes an `audit_log` row, and
3. pages Security through an `audit.Sink` (a `WebhookSink` by default — any provider
   that accepts an inbound JSON webhook works, no vendor SDK).

**This service is not a redaction step.** It never mutates, masks, or quarantines a
capture, a capture event, or an asset — see `internal/audit/service_readonly_test.go`,
which proves that against a real Postgres by hashing every captures/capture_events row
before and after a run that finds a genuine seeded leak, and requiring the hashes to be
identical. Treating this as a filter (e.g. "helpfully" scrubbing what it finds) would
hide exactly the signal it exists to produce — see CLAUDE.md invariant 7.

## Why two ruleset versions per finding

Every finding carries **both**:

- `applied_ruleset_version` — the ruleset the *client* actually redacted with
  (`captures.applied_ruleset_version`, Task 3). May be absent for pre-versioning
  captures.
- `evaluation_ruleset_version` — the ruleset *this audit run* evaluated against.

Without the pair, a finding raised after Security ships a new pattern can't be told
apart from a genuine client-side redaction hole: both look identical as "the current
ruleset flags this capture." The pair makes that classification possible, and makes a
finding **reproducible** — re-running `Service.RunWorkspace` with the same
`evaluationVersion` against the same capture always reproduces the same finding.

## Rule-semantic parity with Phase 0

`internal/redact` is a Go port of `packages/capture-core/src/redaction`'s rule
semantics (`field-path`, `header`, `pattern`, `dom-selector`; `video-blur` and
`origin-allow` never match an event, same as the TS engine). It is *detection-only* —
given a ruleset and an event, it answers "would any rule have matched here", not "here
is the redacted value" — which is all the audit needs and is considerably simpler than
reproducing the TS engine's redaction-output logic (no collision-safe key rewriting,
no structured-clone bookkeeping).

Pattern matching uses [`dlclark/regexp2`](https://github.com/dlclark/regexp2) instead
of Go's stdlib `regexp` (RE2): the built-in `phone-id` pattern
(`packages/capture-core/src/redaction/patterns.ts`) uses a negative lookbehind
(`(?<!\d)`), which RE2 cannot express at all. Faithfully porting the *same* PHI
patterns the client ships requires a backtracking-capable engine; this is the one new
dependency this task adds, and it's scoped to `internal/redact`.

**Conformance is proven, not asserted.** `internal/redact/conformance_test.go` loads
`testdata/corpus.json` — a JSON export of the *exact same* fixtures Phase 0's
`packages/capture-core/test/redaction-corpus.test.ts` runs against the TS engine, via
`packages/capture-core/scripts/export-audit-corpus.ts` — and asserts the Go engine
detects the same set of rule IDs as the TS engine did, on every fixture. This is a
generated artifact, not a hand-copied duplicate: regenerate it whenever
`packages/capture-core/src/redaction` or its corpus changes:

```bash
cd packages/capture-core
pnpm export:audit-corpus
```

CI fails loudly (a fixture-count-zero check, then per-fixture rule-set mismatches) if
`testdata/corpus.json` goes stale relative to the corpus it was exported from.

## Layout

```text
internal/redact/   ruleset types, PHI pattern port, detection engine, conformance suite
internal/audit/    Store (narrow slice of sqlcgen.Querier), Sink (alert interface +
                   WebhookSink), Service (poll-once-per-workspace evaluation/finding/alert)
cmd/redaction-audit/  poll-loop entrypoint (no HTTP surface — this is a background job,
                      not a client-facing RPC)
testdata/corpus.json  exported golden corpus (see above)
```

## Running

```bash
export HTR_POSTGRES_DSN=postgres://...
export HTR_RUNTIME_PASSWORD=...            # htr_runtime role password
export HTR_ALERT_WEBHOOK_URL=https://...   # required — the whole point is paging Security
export HTR_REDACTION_AUDIT_INTERVAL=5m     # optional, defaults to 5m
go run ./cmd/redaction-audit
```

## Tests

```bash
go test ./...              # unit (100% coverage) + the read-only-proof integration test
                            # (testcontainers-go/Postgres; skips cleanly with no Docker)
golangci-lint run ./...
```

- `internal/redact/conformance_test.go` — Go/TS parity on every golden-corpus fixture.
- `internal/audit/service_test.go` — a synthetic leak raises exactly one finding/alert
  carrying both ruleset versions; a capture evaluated against an older, pinned ruleset
  version is classified against that version, not the latest; error paths for every
  Store/Sink failure mode.
- `internal/audit/service_readonly_test.go` — the read-only proof: a before/after
  digest of every captures/capture_events row, against real Postgres, across a run that
  finds a genuine seeded leak.
