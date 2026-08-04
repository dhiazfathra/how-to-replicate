# Phase 3 Implementation Plan — mobile and analytics

**Derives from:** [design spec](../specs/2026-08-04-how-to-replicate-design.md) §15, §19 "Phase 3 — mobile and analytics"
**Depends on:** [Phase 2 plan](2026-08-04-phase-2-implementation.md) complete and merged
**Date:** 2026-08-04

Phase 3 adds the first non-browser capture surface and the first cross-capture
analysis. Both are places where the product's scope guard matters more than
usual, so it is restated as a constraint rather than left to judgment.

---

## Global Constraints

### The scope guard (spec §1)

Any feature that does not improve the fidelity of the replication document, or
the speed with which it reaches a human or an agent, is out of scope. This
applies with particular force to Task 4 — see invariant 17.

### Invariants — all thirteen prior ones still bind

1. No unredacted capture is viewable, exportable, or routable. Gate is
   `state === 'ready'`.
2. No unblurred frame exists in a persisted artifact.
3. LLM steps cite event IDs that exist, or they are dropped.
4. Lost fidelity is visible.
5. `packages/capture-core` depends on nothing in `clients/`.
6. The server never becomes the read path for local-first clients.
7. `redaction-audit` is an alarm, not a filter (except the Phase 2 anonymous path).
8. Eviction requires `sync.manifestComplete === true`.
9. Nothing unredacted is transmitted from a client we control.
10. `TRD-SEC-003` gates the anonymous upload path.
11. Anonymous uploads are inspected in a non-durable buffer; failures are rejected outright.
12. The MCP projection is read-only.
13. Every MCP read writes an `audit_log` entry with the resolved agent identity.
14. One retry-and-backoff implementation — the transactional outbox.

### Invariants added by Phase 3

15. **iOS captures are `fidelity: 'degraded'` by definition.** No CDP equivalent
    exists on iOS, so network and console capture require SDK-level
    instrumentation of the app's HTTP client and will always be partial. Every
    iOS capture is stamped degraded and labelled as such in the viewer. Do not
    add a code path that stamps an iOS capture `full`, however complete the
    instrumentation gets.
16. **Redaction runs on-device, with the same ruleset format.** The iOS client is
    a client we control (invariant 9): nothing unredacted is transmitted, and the
    ruleset bundle is the same versioned format the browser uses — not a
    reimplementation with its own rule vocabulary.
17. **Pattern detection reads captures; it does not become session-replay
    analytics.** Heatmaps, funnels, and cohort analysis are explicit non-goals
    (spec §3). Detection groups existing captures by shared failure signature.
    If a proposed feature would work on users who never filed a capture, it is
    out of scope.
18. **No native Android capture.** Not planned (spec §3). Do not add it, do not
    abstract "for Android later".

### Conventions

- Client-minted ULIDs; events are ms offsets from `capture.epoch`; timelines and
  assets immutable.
- The iOS client syncs through the existing `sync-gateway` protocol. It does not
  get its own ingest endpoint.
- Pattern detection is a read-side service. It never mutates a capture.

### Engineering standards

- **100% coverage of branches and edge cases** — Swift, Dart, Go, and TypeScript.
- Lint clean before DONE: `swiftlint`, `dart analyze`, `golangci-lint run`,
  `pnpm lint`.
- Conventional commits. Update the README for anything affecting setup or usage.
- No new runtime dependency beyond the ones this plan names.

### Stack decisions (already made — do not re-decide)

| Concern | Decision |
|---|---|
| iOS capture | ReplayKit **broadcast upload extension** (system-wide), Swift |
| iOS UI | SwiftUI for the capture-control surface |
| Host app integration | Flutter-side SDK shim (the host apps are Flutter, per spec §15) |
| Bridge | Pigeon-generated platform channels — no hand-written channel plumbing |
| iOS storage | App Group container shared between app and broadcast extension |
| iOS tests | XCTest; Dart side `flutter_test` |
| Pattern detection | Go service, reusing Phase 1 `services/internal` |
| Signature clustering | Deterministic signature + similarity threshold. No ML model |

### Repository layout added by this phase

```text
clients/
  ios/                ReplayKit broadcast extension + SwiftUI control surface
packages/
  flutter-sdk/        Dart SDK shim: metadata, interaction trail, HTTP instrumentation
services/
  patterns/           recurring-bug pattern detection
integrations/
  helpdesk/           helpdesk plugin
```

---

## Task 1: iOS ReplayKit capture and on-device redaction

Spec §15. The broadcast extension runs in a **memory-constrained** process
(historically ~50 MB); that constraint shapes every decision here and is not
negotiable by writing more efficient code alone.

**Deliverables** in `clients/ios/`:

- A **ReplayKit broadcast upload extension** receiving video sample buffers, plus
  a SwiftUI control surface in the host app for arming, starting, and stopping.
- **Blur is composited pre-encode** (invariant 2): sample buffers are drawn
  through a Core Image / Metal blur over the region list before they reach the
  asset writer. The unblurred buffer is never written and never leaves the
  process. If the compositor misses its frame budget, **degrade to
  screenshot-only** — never emit unblurred video.
- Region resolution: the Flutter side (Task 2) supplies masked-widget rectangles
  in screen coordinates, re-sampled as layout and scroll change. A region list
  that fails to resolve **stops the recording** rather than emitting an unblurred
  frame.
- **On-device redaction** (invariant 16) over the event stream and metadata,
  using the same versioned ruleset bundle format as the browser. Port the rule
  semantics faithfully and prove agreement with a conformance suite driven by the
  **same golden corpus fixtures** as Phase 0 Task 4. A divergence between
  implementations is a hole.
- Instant Replay equivalent: a bounded rolling buffer under the extension's
  memory ceiling. Choose the window from the measured ceiling rather than
  copying the browser's 120 s, and state the measured number in the report.
- Storage in an App Group container shared with the host app; the extension writes,
  the app reads and syncs.
- Every capture stamped `source: 'ios'`, `fidelity: 'degraded'` (invariant 15).

**Tests:** XCTest over the redaction conformance corpus; the asset writer proven
to receive only composited buffers; frame-budget degradation triggering
screenshot-only exactly once and never re-enabling video; a region-resolution
failure stopping the recording; memory-ceiling behavior under a synthetic long
capture.

---

## Task 2: Flutter SDK shim — metadata, interaction trail, HTTP instrumentation

Spec §15. This is where the "degraded by definition" fidelity is actually
earned: it supplies what ReplayKit cannot see.

**Deliverables** in `packages/flutter-sdk/`:

- `metadata()` mirroring the Phase 1 JS SDK surface, and **redaction-scanned the
  same way** — a host app passing a patient name into metadata must not create a
  leak.
- **Interaction trail:** taps, text input, navigation, and scroll, captured as
  descriptors (not live widget references), feeding the same deterministic step
  generator vocabulary the browser uses so `Tapped "Save changes" on /patients/123/edit`
  comes out with the same shape.
- **Target naming** in the same priority order as Phase 0 Task 7, adapted to
  Flutter: semantics label → associated label → trimmed text → widget key →
  widget type path.
- **HTTP client instrumentation:** an interceptor over the app's HTTP client
  producing `NetworkPayload`-shaped events. Same body handling as the browser —
  32 KB truncation, content-type allow-list, field and header redaction — because
  it is the same ruleset.
- **Console equivalent:** capture `debugPrint` / logging output and uncaught
  Flutter errors as `console` events, including the stack.
- Masked-widget marking for blur: a `HtrMask` widget (and a `data-phi` equivalent
  convention) whose rectangles are handed to Task 1's compositor each frame.
- Platform channels generated with Pigeon. No hand-written channel plumbing.
- The SDK must fail **safe and quiet**: if the native side is unavailable, the
  host app keeps working and no capture is produced. It must never crash a
  clinic-facing application.

**Tests:** `flutter_test` over metadata redaction with corpus fixtures; naming
priority at each fallback level; interceptor truncation and content-type rules;
the native-unavailable path leaving the host app functional; a masked widget's
rect reaching the channel.

---

## Task 3: iOS sync and viewer support

Closes the loop: an iOS capture must be indistinguishable from a browser capture
everywhere downstream, except in its honestly-degraded fidelity.

**Deliverables**

- The host app syncs through the **existing** `sync-gateway` protocol from
  Phase 1 — same mutation vocabulary, same client-minted ULIDs, same asset upload
  with manifest verification. No iOS-specific ingest endpoint.
- A mutation queue with the same durability guarantees as the browser's: survives
  app termination, capped, with a visible pending-sync indicator rather than
  silent overflow.
- Viewer support in `clients/viewer`: an iOS capture renders its timeline,
  document, and video like any other, with the `fidelity: 'degraded'` badge
  prominent and a specific explanation that iOS has no CDP equivalent — a
  generic badge is not enough for a reader to know what is missing.
- `withheldEventCount` rendering unchanged.
- The replication document for an iOS capture comes from the same deterministic
  generator, and its LLM enrichment obeys the same citation validation
  (invariant 3).

**Tests:** an iOS capture round-trips through sync identically to a browser one;
the queue survives a simulated app kill; the viewer renders the iOS-specific
degraded explanation; document generation over an iOS timeline matches the
shared golden fixtures.

---

## Task 4: services/patterns — recurring-bug detection across brand surfaces

Spec §19 Phase 3. Read invariant 17 before starting: this is capture clustering,
not analytics.

**Deliverables** in `services/patterns/`:

- A **deterministic failure signature** derived from a capture: normalized error
  message and type, normalized stack frames (app frames only, paths and line
  numbers stripped to stable identifiers), the failing network endpoint pattern,
  and the terminal interaction. Deterministic means two captures of the same bug
  produce the same signature — no model, no embedding, no nondeterminism.
- Clustering by exact signature first, then a similarity threshold for
  near-matches. Document the threshold and how it was chosen; make it
  configurable per workspace.
- Cross-**brand-surface** grouping: a cluster spanning multiple `projectId`s is
  the high-value output, because it means one defect is hitting several brands.
  Surface that explicitly rather than leaving it to be noticed.
- Output: a cluster record with member captures, first and last seen, affected
  projects, and a representative capture. **Read-only** over captures — the
  service never mutates one.
- Viewer surface: a clusters view listing recurring bugs, each linking to its
  member captures.
- **Explicitly not built:** heatmaps, funnels, cohort analysis, per-user
  journeys, or anything computed over users who filed no capture (spec §3
  non-goals, invariant 17). If a stakeholder asks for one during this task,
  report it rather than building it.

**Tests:** signature determinism across two captures of the same synthetic bug;
signature divergence across genuinely different bugs; stack normalization
stripping volatile parts and keeping stable ones; cross-project cluster
detection; the service proven to perform no write to capture data.

---

## Task 5: Helpdesk plugin

Spec §19 Phase 3. Puts capture where support agents already work.

**Deliverables** in `integrations/helpdesk/`:

- A plugin embedding into the existing helpdesk product so a support agent can:
  1. send a **Recording Link** to a customer from inside a ticket (Phase 2
     server-backed path, so no download step for the customer), and
  2. see the resulting capture attached to that ticket when it arrives.
- The ticket association is carried by the link token's scope, not by asking the
  customer to paste an ID.
- Rides the Phase 1 **transactional outbox** for delivery back to the helpdesk
  (invariant 14). No second delivery mechanism.
- The plugin renders the replication document and a signed, expiring video link.
  It never receives raw assets and never a capture that is not `ready`.
- **The helpdesk product is a decision, not an assumption.** Confirm which
  product and which extension mechanism before implementing; if unknown, report
  `BLOCKED` with the question rather than picking one.
- OAuth without a shipped client secret, consistent with every other integration.

**Tests:** ticket-scoped token association; a non-`ready` capture never renders;
signed link expiry; delivery through the outbox including the crash-and-restart
path; a revoked link refused.

---

## Out of scope for this plan

- **Native Android capture.** Not planned (spec §3, invariant 18).
- Session-replay analytics of any kind — heatmaps, funnels, cohort analysis
  (spec §3, invariant 17).
- Our own issue tracker. We integrate; we do not replace (spec §3).
- Billing, plans, or usage metering (spec §3).
- Server-side transcoding on the ingest or redaction path (spec §3).
- Any write verb on the MCP surface (invariant 12).
