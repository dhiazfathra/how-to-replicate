# ADR-004: One append-only capture-event timeline

## Status

Accepted

## Date

2026-08-04

## Context

A capture gathers several streams of ambient browser state: console messages,
network requests, user interactions, navigations, plus internally-generated
lifecycle markers (state transitions, fidelity changes) and reporter annotations.
The obvious modelling is one store per stream — `console_logs`, `network_requests`,
`interactions` — mirroring how the browser itself exposes them.

But every consumer of this data needs them interleaved:

- **Playback** scrubs a video and must show what was happening at that instant
  across every stream.
- **Step generation** (deterministic and LLM) reads a chronological narrative:
  clicked, then request failed, then error logged.
- **MCP export** hands an agent an ordered account of what happened.
- **The viewer's timeline** is one visual track, not several.

With separate stores, every one of those consumers implements its own merge, and
each merge needs its own answer to "what happens when a console message and a
network response share a millisecond?" Several consumers, several chances to answer
differently, and the bugs that result are ordering bugs — the kind that reproduce
once in fifty runs.

## Decision

**One append-only log, `capture_event`, holds every kind in a single ordered
stream.**

```ts
type CaptureEvent = {
  id: string;      // monotonic ULID — sort key needs no tiebreaker
  captureId: string;
  t: number;       // ms offset from capture.epoch
  kind: 'console' | 'network' | 'interaction' | 'navigation'
      | 'lifecycle' | 'annotation';
  payload: /* discriminated on kind */;
  redaction: { rulesApplied: string[]; fidelity: 'full' | 'redacted' | 'dropped' };
};
```

Two properties are load-bearing:

**Ordering is intrinsic.** Monotonic ULIDs (ADR-003) mean lexicographic sort of
`id` is the canonical order — the order each source's callback fired in, not
necessarily true causal order across sources (ADR-003 §Context). No consumer
merges anything; no consumer needs a tiebreaker.

**Events are immutable.** Once written, an event is never updated or deleted.
Retention purges an entire capture, not individual events. Redaction happens *before*
the write, never as an edit afterwards (ADR-005).

Per-kind payload shapes stay strongly typed via a discriminated union, so the single
table does not mean untyped blobs.

## Alternatives Considered

### Separate stores per event kind

- Pros: Natural fit to the browser's own APIs. Narrower types per store. Kind-scoped
  queries need no filter.
- Cons: Every consumer reimplements the merge, and each merge is a fresh opportunity
  to order same-millisecond events differently. Adding a fifth event kind means
  touching every consumer.
- Rejected: The interleaved read is the dominant access pattern; the model should
  serve it directly.

### One store, but with a mutable `redacted` flag toggled after the fact

- Pros: Redaction could be re-run with an updated ruleset over already-captured
  events.
- Cons: Requires the raw event to be stored so it can be redacted later — meaning
  unredacted PHI at rest in IndexedDB, which is exactly what ADR-005 forbids.
- Rejected: Directly incompatible with the redaction model.

### Store the raw CDP protocol stream verbatim, normalise on read

- Pros: Lossless. Nothing is discarded, so new derivations remain possible.
- Cons: Redaction cannot be applied on ingest without normalising anyway, so this
  again implies raw PHI at rest. Also couples storage to the CDP wire format, which
  Chrome revises, and makes the `webRequest` fallback path (ADR-006) unrepresentable
  in the same store.
- Rejected: Two blocking problems — PHI at rest, and no shared shape between the CDP
  and fallback capture paths.

## Consequences

- **Immutability makes sync nearly trivial.** Events can never conflict, so the
  entire conflict surface reduces to four mutable scalar fields plus append-only
  comments. This is what makes last-write-wins sufficient and a CRDT unnecessary
  (ADR-012). This consequence is the single largest simplification in the system,
  and it follows from immutability rather than from any cleverness in the sync layer.
- **Steps can cite evidence.** `Step.eventIds` references entries in one log, which
  makes steps clickable in the viewer and makes LLM output verifiable — a cited ID
  either exists or it does not (ADR-005, §8.3 of the spec).
- **Re-redaction of existing captures is impossible.** If a ruleset gap is found,
  affected captures must be deleted, not re-processed — there is no raw copy to
  re-run rules against. This is a deliberate trade: the alternative is raw PHI at
  rest. Ruleset gaps are handled by deletion plus the Phase 1 `redaction-audit`
  alarm.
- **Kind-scoped queries need an index on `(captureId, kind)`** in both IndexedDB and
  the Phase 1 Postgres mirror. Cheap, but not free — the network-requests-only view
  is a filtered scan rather than a dedicated store.
- **Payload sizes vary by two orders of magnitude** between an interaction event and
  a truncated network body. The Postgres mirror should therefore partition by
  `captureId` rather than assume uniform row width.
- Splitting this into per-kind stores later would be mechanical but would require
  rewriting every consumer's read path, and would reintroduce the merge-ordering
  problem this decision exists to remove.
