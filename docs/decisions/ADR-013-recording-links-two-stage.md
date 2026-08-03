# ADR-013: Recording Links ship in two stages — export-only, then server-backed

## Status

Accepted

## Date

2026-08-04

## Context

Recording Links let an external clinic customer report a bug with no login and no
install: they open a URL, record their screen, and submit. PRD-JAMCLONE-001 places
them in Phase 1, and they are the mechanism by which all 13+ brand surfaces are meant
to collect customer-reported issues.

They collide directly with ADR-002. An anonymous external reporter has no local
workspace, no account, and no reason to keep a capture in their own browser. The
capture has to go *somewhere* — which means a server we operate, which is exactly what
Phase 0 defers.

The collision is not merely a sequencing inconvenience. A server receiving captures
from unauthenticated external browsers is the highest-risk component in the entire
system:

- The reporter's machine is not a trust boundary we control, so client-side redaction
  cannot be *relied* upon there the way it can on a managed QA machine.
- Anonymous upload endpoints attract abuse and need rate limiting, size limits, and
  content validation.
- It is customer-facing PHI handling, which is precisely what `TRD-SEC-003` exists to
  review.

Meanwhile the capture path itself — `getDisplayMedia`, redaction, document generation
— is the same code the extension uses, and it needs validating against real external
browsers on real external machines regardless of where the result goes.

## Decision

**The feature ships twice, deliberately.**

### Phase 0 — export-only handoff

The Recording Link page captures locally via `getDisplayMedia`, applies the same
ruleset through `packages/capture-core`, generates a replication document, and
produces a downloadable **`.htr` bundle** — video, timeline JSON, and rendered
markdown — that the reporter attaches to an email or an existing support ticket.

No server. **The page itself performs no network upload** — that narrower claim is
what actually holds, not the broader one that no customer PHI handling occurs on
our side at all. The `.htr` bundle is customer video and timeline data, and once
the reporter attaches it to an email or an existing support ticket, it enters
whatever channel receives that email or ticket — a system we do operate and do
control, even though the Recording Link page didn't put it there directly. The
approved intake channel is the existing support-ticket system (already covered by
its own access, retention, and review posture as an email/ticketing system); this
ADR does not carve out a new exemption for `.htr` attachments arriving through it.
`TRD-SEC-003` gates the **network-facing** upload path specifically — Phase 2's
`sync-gateway` endpoint receiving bytes directly from an anonymous browser — because
that is the new capability this feature introduces, not the general fact that
customer data can reach support systems through normal channels.

### Phase 2 — server-backed

The same page, upgraded: token-scoped anonymous upload directly into `sync-gateway`,
no download step. Server-side `redaction-audit` runs before the capture becomes
viewable — a stricter posture than for internal captures, because the reporter's
machine is outside our control.

Critically, redaction here must happen **before any durable write**, not merely
before viewability. The naive reading — "audit runs before `ready`" — would still
let raw uploaded bytes sit durably in `sync-gateway`'s ingest storage or the object
store for however long the audit takes to run, which is exactly the "unredacted
PHI at rest" outcome ADR-005 forbids. So the actual pipeline is: bytes land in a
**non-durable inspection buffer** (in-memory or a short-TTL scratch area, not the
retained object store), the ruleset applies there, and only a **redacted** result
is ever written to durable storage. If inspection or redaction fails for any
reason, the upload is rejected outright — there is no path where raw source bytes
are retained "for later" redaction or re-processing; that would recreate the exact
re-redaction problem ADR-004 already ruled out for the authenticated path.

**`TRD-SEC-003` is a hard prerequisite** for this stage.

The Phase 0 stage is not a throwaway. It shares `capture-core` with the extension, so
the majority of it is code Phase 2 keeps. What Phase 2 replaces is the final step —
download becomes upload.

## Alternatives Considered

### Keep Recording Links in Phase 1, make them the first server

- Pros: Matches the PRD. External customer reporting arrives two phases sooner, which
  is where a large share of the business value sits.
- Cons: Inverts the architecture. The first server would be built for the anonymous
  flow, before the sync engine exists — so Phase 1 would carry two data paths
  (server-first anonymous, local-first authenticated) with different trust models and
  different code, maintained in parallel from the start. It also front-loads
  `TRD-SEC-003` onto a capture format that has not yet stabilised against real
  captures, meaning the security review examines a moving target.
- Rejected: The sequencing cost is structural, not just schedule.

### Defer Recording Links entirely to Phase 2, nothing in Phase 0

- Pros: Cleanest. One implementation, no two-stage story, no export flow to build and
  then partly discard.
- Cons: The `getDisplayMedia` capture path, redaction on unmanaged machines, and
  browser-compatibility behaviour all go unvalidated until Phase 2 — at which point
  they arrive together with the anonymous upload endpoint and the security review.
  Discovering that redaction behaves differently on an unmanaged consumer browser is
  much cheaper in Phase 0 than during a Phase 2 security review.
- Rejected: The Phase 0 stage buys real de-risking for modest cost.

### Route external captures through the reporter's email as an attachment automatically

- Pros: No server, and no manual download step for the reporter.
- Cons: Requires a `mailto:` with a multi-megabyte attachment, which is not a thing
  browsers can do. Any automated send needs a server.
- Rejected: Not technically possible.

### Have external reporters install the extension

- Pros: One capture surface. Full CDP fidelity.
- Cons: A clinic customer will not install a browser extension to report a bug, and
  the `chrome.debugger` infobar (ADR-006) is alarming to non-technical users. The
  entire premise of Recording Links is zero install.
- Rejected: Defeats the purpose.

## Consequences

- **External customer reporting stays manual until Phase 2.** This is the real cost,
  and it is the PRD success metric most affected — "all 13+ brands using Recording
  Links within two quarters" now measures from the Phase 2 release rather than
  Phase 1. Stated plainly so the metric is not quietly missed.
- **The Phase 0 export flow has genuine friction.** The reporter downloads a file and
  attaches it somewhere. That is worse than one click, and it is worth being honest
  that this stage is a validation vehicle first and a user-facing feature second.
- **The `.htr` bundle format becomes a supported artifact.** Once external users have
  files on disk, the format needs a version field and an importer that reads older
  versions. It also doubles as Phase 0's only durable backup for internal captures
  (ADR-009), which makes it more load-bearing than it first appears.
- **Recording Link captures are `fidelity: 'degraded'` by construction.** No CDP is
  available on a page we do not control, so console and network capture rely on
  monkey-patching within the shared page context (ADR-006). External reports carry
  less technical context than internal ones, and the viewer must show that clearly so
  an engineer calibrates trust correctly.
- **Phase 2 needs abuse controls the extension path never needed:** rate limiting per
  link token, upload size caps, content-type validation, and link expiry and
  revocation. None of that exists in Phase 0 because there is nothing to abuse.
- **Two entry points to maintain in Phase 2** — authenticated sync and anonymous
  upload — but they converge on one ingest path with different authorisation, rather
  than being two parallel implementations. That convergence is the thing the two-stage
  ordering protects.
