# ADR-005: Client-side redaction as primary control, behind a fail-closed gate

## Status

Accepted

## Date

2026-08-04

## Context

Dermaesthetics is healthcare-adjacent and operates under ISO 27001 and Indonesian
PDP Law. Patient data will appear in captures — in DOM text, in network request and
response bodies, in headers, and visibly on screen in recorded video. This is not a
hypothetical; a capture taken on a patient-record screen contains PHI by
construction.

PRD-JAMCLONE-001 §6 places redaction in a server-side async worker, following the
reference product's architecture. Two facts make that placement wrong for us:

1. **Phase 0 has no server** (ADR-002). There is no worker to run a server-side
   pass in.
2. **Video blur cannot be applied after encoding** without a server-side transcode
   step, which is explicitly out of scope. Blur must be composited into the frames
   before `MediaRecorder` sees them — which can only happen on the client.

The second fact holds in every phase, not just Phase 0. Even with a full server
platform, server-side video blur would require decoding, blurring, and re-encoding
every recording — and in the window before that completes, an unblurred video of a
patient record exists at rest on our infrastructure.

There is a third consideration. Redaction placed after the network boundary is
redaction that happens *after the incident*. Once unredacted PHI has left the
reporter's browser, the disclosure has occurred; scrubbing it server-side changes
what is stored, not what was transmitted.

## Decision

**Redaction runs on the client, before anything is persisted or transmitted, and the
capture pipeline fails closed.**

Three parts:

**1. Client-side and primary.** The ruleset is applied in `packages/capture-core`
before any write to IndexedDB and before any byte crosses the network. Video blur is
composited onto an `OffscreenCanvas` whose stream feeds `MediaRecorder`, so no
unblurred frame ever exists in a persisted artifact. Only the live in-memory display
stream is unblurred.

**2. Applied continuously while recording, not on finalize.** The Instant Replay
ring buffer holds already-redacted events. A reporter who records for two minutes and
never triggers a capture must not leave raw PHI sitting in extension memory.

**3. Fail closed, at every failure point.**

| Failure | Behaviour |
|---|---|
| A rule cannot be applied to an event | Event is **dropped**, not stored raw. `withheldEventCount` increments. |
| Blur compositor misses frame budget | Capture **degrades to screenshot-only**. Never emits unblurred video. |
| Redaction stage errors | Capture enters `failed`. Not viewable, not exportable, not routable. |
| Origin is not on the allow-list | **No capture at all** — not video, not screenshot, not metadata. |

The gate: export, share, route, and MCP read all require `state === 'ready'`. No
code path surfaces a capture in `recording`, `redacting`, `composing`, or `failed`.

The Phase 1 `redaction-audit` service re-runs the ruleset server-side and **alerts on
any finding**. It is explicitly an alarm, not a filter — by the time data reaches it,
unredacted content has already crossed the network boundary and the incident has
already happened. Its job is to detect ruleset gaps and page Security, not to make
data safe.

## Alternatives Considered

### Server-side redaction worker, per the PRD

- Pros: Centralised ruleset enforcement with no client trust required. Ruleset
  updates apply without shipping an extension release. Re-redaction of existing
  captures becomes possible.
- Cons: Impossible in Phase 0. Requires server-side transcode for video blur, which
  is out of scope. And it moves redaction to after the network boundary, so
  unredacted PHI is transmitted and stored — briefly — in every case.
- Rejected: The transmission itself is the disclosure. Redacting afterwards changes
  the record, not the event.

### Client-side redaction with a server-side second pass that also filters

- Pros: Defence in depth, with the server able to catch client-side gaps.
- Cons: Creates a dangerous ambiguity about which layer is responsible. If the server
  is understood to be a filter, client-side gaps get treated as tolerable — and in
  Phase 0 there is no server pass at all, so that tolerance would be misplaced.
- Rejected in this framing, and adopted in a different one: the server pass exists,
  but strictly as an alarm. The distinction is not pedantic — it determines whether
  a client-side gap is treated as a bug or as an incident.

### Fail open — capture and store raw when redaction cannot be applied, flag for review

- Pros: No data loss. A reviewer can decide what is safe.
- Cons: Puts unredacted PHI at rest and makes disclosure contingent on a human
  reliably reviewing a queue. Under PDP Law the storage itself is the violation,
  regardless of whether anyone later looks.
- Rejected: Losing a console line is recoverable. Storing a patient record is not.

### Blur applied at playback time rather than at encode

- Pros: Simple, no compositing cost during capture, blur regions adjustable later.
- Cons: The stored video is unblurred. Anyone with the blob — or with the object
  storage credentials, or a copy of the exported bundle — bypasses the blur entirely.
  It is a UI affordance masquerading as a control.
- Rejected: Not a security measure.

## Consequences

- **The golden synthetic-PHI corpus is load-bearing.** With no server-side filter,
  that test suite is what stands between a capture and a PHI leak. It gates every
  PR, covers Indonesian NIK, BPJS, MRN, DOB, phone numbers, and patient names across
  JSON bodies, headers, DOM text, and video frames, and it is reviewed by
  Security/Compliance rather than Eng alone.
- **Video blur costs CPU during capture.** Canvas compositing on the lowest-spec QA
  machine is unmeasured; if it consistently misses frame budget, screenshot-only
  becomes the default on that hardware. Measured during Phase 0 (spec §22).
- **Captures will have gaps, and gaps must be visible.** `withheldEventCount`
  renders as "N events withheld by redaction policy". A quietly incomplete document
  that looks complete is worse than an obviously incomplete one, because an engineer
  would trust it.
- **Ruleset updates require an extension release** for policy-pinned rule changes,
  making the release cadence a compliance dependency. Mitigated by fetching ruleset
  bundles on a timer, but the bundle format and signature verification ship with the
  extension.
- **Existing captures cannot be re-redacted** — there is no raw copy (ADR-004). A
  discovered ruleset gap is remediated by deleting affected captures.
- **Ownership split:** Security/Compliance owns ruleset contents; Engineering owns
  the enforcement mechanism and its test corpus. This resolves PRD open question #4.
- `TRD-SEC-003` remains a hard prerequisite for the Phase 2 server-backed Recording
  Link. Phase 0's export-only variant does not gate on it, because nothing is
  transmitted to us.
