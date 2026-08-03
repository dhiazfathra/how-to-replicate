# How to Replicate — agent instructions

Bug-capture tool. Every capture produces one artifact: a **How to Replicate
document** — ordered repro steps, each citing the events that evidence it.

**Repository is at design stage.** Spec and ADRs only, no implementation. Read
[the spec](docs/superpowers/specs/2026-08-04-how-to-replicate-design.md) and
[ADR-001](docs/decisions/ADR-001-local-first-architecture.md) /
[ADR-002](docs/decisions/ADR-002-client-only-phase-0.md) before proposing anything —
every other decision is downstream of those two.

## Decisions already made — don't re-decide these

Check [docs/decisions/](docs/decisions/README.md) before proposing an architectural
change. All 14 ADRs record the rejected alternatives and why. Common re-litigations,
already settled:

- **"Add a small service for X in Phase 0."** No. Phase 0 operates no service of our
  own (ADR-002). The compliance surface of a service holding capture data is not
  proportional to its line count.
- **"Use a CRDT for sync."** No. Timelines and assets are immutable, so the conflict
  surface is four scalar fields. Last-write-wins is sufficient (ADR-012).
- **"Redact server-side."** No. Video blur must be pre-encode, and redaction after the
  network boundary is redaction after the disclosure (ADR-005).
- **"Let the LLM write the repro steps."** Only on top of the deterministic floor, and
  only with citation validation (ADR-007).
- **"Store the raw event stream and redact later."** No — that means unredacted PHI at
  rest (ADR-004).

## Invariants

Breaking 1 or 2 is a compliance incident, not a bug:

1. No unredacted capture is viewable, exportable, or routable. The gate is
   `state === 'ready'`.
2. No unblurred frame exists in a persisted artifact. Blur feeds `MediaRecorder`, not
   the player. Missed frame budget degrades to screenshot-only, never to unblurred
   video.
3. LLM steps cite event IDs that exist, or they are dropped.
4. Lost fidelity is visible — `fidelity: 'degraded'`, `withheldEventCount`.
5. `packages/capture-core` depends on nothing in `clients/`.

## Conventions

- **Local-first.** The UI reads the local observable store. Never add a fetch on a
  path where data is already local. Mutations apply locally and synchronously, then
  enqueue.
- **Event timestamps are offsets from `capture.epoch`**, not wall-clock times. Do not
  introduce wall-clock comparisons inside a capture.
- **Client-minted ULIDs** for all identity. No server round trip to create anything.
- **Animate only `transform` and `opacity`.** Never `width`, `height`, `margin`, `top`,
  or `left`.
- **Go services** (Phase 1+): chi + otelhttp, ConnectRPC for browser clients, gRPC
  internally.
- 100% coverage of branches and edge cases. Run the linter before concluding work.

## The redaction corpus

The golden synthetic-PHI corpus (spec §18) is the most important test asset here.
Phase 0 has no server-side backstop, so it is what stands between a capture and a PHI
leak. It gates every PR. Do not weaken, skip, or mark it flaky — if it fails,
the code is wrong.
