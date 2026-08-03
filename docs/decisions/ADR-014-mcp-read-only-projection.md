# ADR-014: MCP server exposes captures as a read-only projection

## Status

Accepted

## Date

2026-08-04

## Applies from

Phase 2.

## Context

PRD-JAMCLONE-001 §5.4 calls for an MCP server so coding agents — Claude Code,
internal agentic tooling — can pull full bug context into an agent session: the
replication document, event timeline, network requests, environment snapshot, and
asset references. This is the surface that turns a capture from a human artifact into
something an agent can act on autonomously.

The design question is what access an agent gets. MCP supports both resources
(read) and tools (which may write). It would be straightforward to expose tools that
let an agent update a capture — mark it triaged, correct a repro step, attach an
analysis, close it as resolved.

Two things argue against that, and one of them is about evidence rather than about
agents.

**A capture is a record of what happened.** Its value depends on being an unmodified
account. An agent that "corrects" a repro step has altered the evidence a human will
later rely on, and there is no way to tell from the artifact that it happened. This is
the same reason the event timeline is immutable (ADR-004) — but here the concern is
specifically that the modification would be invisible and confidently wrong in a way
that is hard to detect.

**Agent identity in audit logs is weaker than human identity.** An agent acts on
behalf of a user, through a token, possibly in an automated pipeline with no human
present. For a read that is a manageable attribution problem. For a write it means the
audit log records that *something* changed a capture, with a chain of delegation that
may be several hops long.

There is also a compliance dimension: captures contain PHI-adjacent data, and MCP is a
new egress path for it — one whose consumers are, by design, general-purpose agents
that may forward context to a model endpoint.

## Decision

**The MCP server is read-only. Resources, no mutating tools.**

Exposed resources:

- The replication document — title, summary, steps with their event citations
- The event timeline, filterable by kind and time range
- Network requests, with redaction already applied
- Environment snapshot and SDK-injected metadata
- Asset references as short-lived signed URLs (ADR-011)

Not exposed: any operation that writes to a capture. An agent that wants to record a
finding does so where findings belong — a tracker issue via `router`, or a new capture
via the CLI (`source: 'cli'`), which appends a separate artifact rather than editing an
existing one.

Additionally:

- **Every MCP read writes an `audit_log` entry** with the resolved agent identity, the
  capture accessed, and which resources were read.
- **Only `state === 'ready'` captures are visible**, same gate as every other consumer
  (ADR-005). Redaction has completed or the capture does not exist as far as MCP is
  concerned.
- **Workspace RBAC applies** to the acting identity. An agent sees exactly what the
  user it acts for would see — never more.

## Alternatives Considered

### Read-write MCP with mutating tools

- Pros: Agents could triage autonomously — label, assign, mark duplicate, close.
  That is a genuinely appealing workflow and is where agentic SDLC tooling is heading.
- Cons: Agents modify evidence, invisibly. An incorrect "correction" to a repro step is
  worse than no correction, because a human then trusts it. The audit story becomes
  hard to reason about across delegation hops. And it is a one-way door — once
  agents write to captures, tightening later means breaking workflows people have built.
- Rejected for now on the evidence-integrity argument. Worth revisiting for a narrow,
  clearly-bounded set of writes — triage labels and assignment, never document content
  — once agent identity in the audit log is strong enough to attribute a write to a
  specific run.

### Expose captures as MCP tools rather than resources, even for reads

- Pros: Tools allow parameters, so filtering and search are more expressive than static
  resource URIs.
- Cons: Blurs the read/write distinction that this decision rests on. A future
  mutating tool would be indistinguishable in shape from an existing read tool, which
  makes the boundary a matter of naming discipline rather than structure.
- Partially adopted: resources for capture data, with read-only *query* tools for
  search and filtering where resource URIs are insufficient. The constraint is
  structural — no tool mutates state — not merely conventional.

### Let agents read directly from Postgres or the object store

- Pros: No MCP service to build. Maximum flexibility.
- Cons: No audit trail, no RBAC, no `ready`-state gate, no redaction guarantee, and
  agents coupled to the internal schema. Every control this ADR establishes would be
  bypassed.
- Rejected: The service exists to be the enforcement point.

### Serve MCP from the client rather than a server

- Pros: Works in Phase 0 with no server. Capture data never leaves the machine — which
  aligns with the local-LLM reasoning in ADR-007.
- Cons: The agent must run on the same machine as the browser holding the captures, and
  the extension must expose a local endpoint or native messaging bridge. Workable but
  narrow, and it does not serve CI pipelines or agents running anywhere else.
- Deferred rather than rejected. Phase 2 ships the server-side projection; a local
  MCP bridge over the existing native messaging host (ADR-007) is a plausible later
  addition for the highest-sensitivity surfaces, and would reuse the same resource
  shapes.

## Consequences

- **Agents cannot close the loop within the capture.** An agent that investigates and
  fixes a bug records that in the tracker issue, not on the capture. Arguably the right
  place anyway — the tracker is where resolution state belongs — but it means capture
  and resolution live in two systems, joined by the deep link `router` writes.
- **MCP is a new PHI egress path.** An agent reading a capture may forward that context
  to a model endpoint. Read-only limits blast radius but does not eliminate it. The
  per-read audit log is what makes the egress reviewable, and it is the reason auditing
  reads — not just writes — is mandatory here.
- **Signed URL expiry interacts badly with long agent sessions.** Short expiry
  (ADR-011) means an asset URL fetched at session start may be dead by the time the
  agent uses it. Resources must be re-resolvable, and agents should fetch assets when
  needed rather than caching URLs.
- **Read-only is a much easier security review** than read-write, which matters for
  getting Phase 2 shipped.
- **Audit volume will be high.** An agent reading a capture generates far more audit
  entries than a human browsing one. Retention and indexing on `audit_log` should
  anticipate agent-scale read traffic.
- Relaxing to allow narrow writes later is additive and does not break existing
  consumers. Going the other direction — retracting write access agents already depend
  on — would. Starting read-only is the reversible choice.
