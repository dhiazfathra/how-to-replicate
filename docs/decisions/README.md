# Architecture Decision Records

Decisions that would be expensive to reverse, with the context and rejected
alternatives that produced them. New decisions get a new ADR; superseded ones are
marked, never deleted.

Format: `ADR-NNN-kebab-case-title.md`, sections `Status` / `Date` / `Context` /
`Decision` / `Alternatives Considered` / `Consequences`. Continue the sequence.

Lifecycle: `Proposed → Accepted → (Superseded by ADR-NNN | Deprecated)`.

| ADR | Decision | Status | Phase |
|---|---|---|---|
| [001](ADR-001-local-first-architecture.md) | IndexedDB is the permanent source of truth; the server is a sync target | Accepted | all |
| [002](ADR-002-client-only-phase-0.md) | Phase 0 ships zero new server-side code | Accepted | 0 |
| [003](ADR-003-client-minted-ulids.md) | Client-minted ULIDs for capture and event identity | Accepted | all |
| [004](ADR-004-append-only-event-timeline.md) | One append-only timeline for console, network, and interaction events | Accepted | all |
| [005](ADR-005-fail-closed-redaction-gate.md) | Client-side redaction as primary control, behind a fail-closed gate | Accepted | all |
| [006](ADR-006-client-side-capture-via-cdp.md) | Capture via CDP, with a fidelity-marked fallback | Accepted | all |
| [007](ADR-007-pluggable-llm-providers.md) | Pluggable LLM providers over a mandatory deterministic floor | Accepted | 0+ |
| [008](ADR-008-tracker-provider-abstraction.md) | Tracker abstraction: GitHub first, GitLab second, secretless auth | Accepted | 0+ |
| [009](ADR-009-local-storage-budget.md) | Storage budget; refuse-to-record before auto-eviction | Accepted | all |
| [010](ADR-010-extension-distribution.md) | Enterprise policy push plus unlisted Web Store | Accepted | 0+ |
| [011](ADR-011-self-hosted-object-storage.md) | Self-hosted S3-compatible object storage on Nutanix | Accepted | 1+ |
| [012](ADR-012-sync-protocol.md) | Mutation queue and delta pull, last-write-wins per field | Accepted | 1+ |
| [013](ADR-013-recording-links-two-stage.md) | Recording Links ship export-only, then server-backed | Accepted | 0, 2 |
| [014](ADR-014-mcp-read-only-projection.md) | MCP exposes captures as a read-only projection | Accepted | 2+ |

## Reading order

ADR-001 and ADR-002 are the root decisions — every other ADR is downstream of one or
both. ADR-004's immutable timeline is what makes ADR-012's sync protocol simple
enough to own, and ADR-005's redaction model is what the rest of the compliance story
rests on. Start there.
