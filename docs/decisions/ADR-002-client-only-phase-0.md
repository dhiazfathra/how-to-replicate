# ADR-002: Phase 0 ships zero new server-side code

## Status

Accepted

## Date

2026-08-04

## Context

PRD-JAMCLONE-001 proposes an MVP spanning a browser extension, a Go ingestion
service, object storage, an async LLM worker, and a GitLab adapter — five
independently deployable pieces before the first user files a bug.

That is a long runway to the first real signal. The riskiest assumptions in this
product are all client-side:

- Can `chrome.debugger` reliably capture console and network data across the
  Chromium variants QA actually uses?
- Can video blur be composited pre-encode without missing frame budget on QA
  hardware?
- Do deterministic repro steps derived from an interaction timeline actually read
  well enough to be useful?
- Will QA engineers adopt a capture tool at all?

None of those need a server to answer. Meanwhile the server-side work — ingestion,
storage, workspaces, RBAC, retention — is comparatively well-understood engineering
whose requirements become clearer once the capture format has stabilised against
real captures.

There is also a compliance dimension. The moment we operate a service that receives
capture data, we own PHI-adjacent data at rest: retention policy, audit logging,
`TRD-SEC-003`, encryption review, access control. Deferring that until the capture
and redaction path is proven means the security review examines a stable artifact
rather than a moving target.

## Decision

**Phase 0 ships no service that we operate.**

"No backend" means we deploy nothing, not that the client is offline. The Phase 0
client may call APIs that already exist and are already governed:

- GitHub and GitLab REST APIs, for issue creation
- The internal LLM gateway, for document enrichment
- A localhost model server or the local `claude` CLI, for on-device enrichment

**The reporter's own IndexedDB is the only durable store this product controls in
Phase 0.** Capture content can also come to rest in destinations we do not
operate — a GitHub or GitLab issue, an internal LLM gateway's own logs, a
support-ticket attachment from the export flow ([ADR-013](ADR-013-recording-links-two-stage.md)) — and those destinations govern their own
retention, access, and audit under whatever policy already applies to them. This
ADR does not, and cannot, make claims about that data once it has left for a
system we don't operate.

The dividing line is operational ownership: we add no new place where capture data
comes to rest under **our** control.

## Alternatives Considered

### Ship the PRD's MVP — extension plus ingestion service plus storage plus worker

- Pros: Matches the reference product's architecture. Recording Links and share
  links are available immediately. No throwaway or two-stage features.
- Cons: Delays first user feedback behind five deployables. Requires the full
  compliance package — retention, audit, `TRD-SEC-003` — before any user has
  validated that the capture path works at all. If `chrome.debugger` capture turns
  out to be unreliable, all of that server work was built for a product that needs
  redesigning.
- Rejected: The sequencing puts the well-understood work before the risky work.

### Client-only, but with a thin blob-upload service

- Pros: Unlocks share links and Recording Links in Phase 0. Small service, low
  operational burden.
- Cons: "Thin" is not the relevant measure. A service holding capture blobs holds
  PHI-adjacent data at rest, which pulls in retention, audit logging, encryption
  review, access control, and `TRD-SEC-003` — the full compliance package,
  regardless of the line count. There is no small version of that obligation.
- Rejected: The compliance surface, not the code size, is what Phase 0 is deferring.

### Local-only with no external calls at all

- Pros: Absolute data containment. Trivially compliant.
- Cons: Drops one-click tracker routing, which is half the product's value — the
  PRD's thesis is capture *plus* frictionless routing. Reduces Phase 0 to a
  screen recorder with logs.
- Rejected: Overshoots. Calling an already-governed tracker API is not the risk
  being managed.

## Consequences

- **Recording Links must ship twice.** An anonymous external reporter has no local
  workspace to keep a capture in, so the no-login flow cannot be server-free.
  Phase 0 gets an export-only handoff; Phase 2 gets the real thing. See ADR-013.
- **No server-side redaction backstop exists in Phase 0.** Client-side redaction is
  the only control, which raises the golden-corpus CI gate from important to
  load-bearing. See ADR-005.
- **Tracker auth must be secretless**, since there is no server to hold a client
  secret. This forces GitHub device flow and GitLab PKCE — which is better practice
  than a pasted PAT regardless. See ADR-008.
- **The LLM path depends on infrastructure we do not control.** The internal gateway
  must accept browser-origin CORS and per-user SSO tokens. Deterministic step
  generation is therefore mandatory as a floor, so Phase 0 is not blocked on that
  dependency. See ADR-007.
- **No cross-device access, no team visibility, no comments in Phase 0.** Captures
  live on one machine. This is a genuine functional gap, accepted deliberately, and
  it is what Phase 1 exists to close.
- **Local storage cannot auto-evict in Phase 0**, because nothing is synced and
  eviction would destroy the only copy. See ADR-009.
- This decision is cheap to reverse: Phase 1 adds services without changing the
  client's read path, precisely because ADR-001 makes the server a sync target
  rather than a source of truth.
