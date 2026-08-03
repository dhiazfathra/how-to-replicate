# ADR-001: Local-first architecture — the client is the source of truth

## Status

Accepted

## Date

2026-08-04

## Context

How to Replicate is a bug-capture tool used dozens of times a day by QA engineers
and support agents. Perceived speed is a feature: a tool that shows a spinner
between "I found a bug" and "the report is filed" gets abandoned in favour of a
Slack screenshot, which is the exact behaviour we are trying to replace.

Two independent forces push the same direction:

1. **Latency.** Every network round trip costs hundreds of milliseconds. A
   conventional read-through architecture pays that cost on every navigation,
   every filter change, every capture list render.
2. **The data is born on the client anyway.** A capture is assembled from video
   frames, console output, network events, and DOM state that exist only in the
   reporter's browser. Unlike a typical CRUD app, the server is not where the truth
   originates — it is a place the truth might later be copied to.

The second point is the decisive one. In most applications, local-first is an
optimisation layered over a server-authoritative model. Here, server-authoritative
would be the layer — and an inverted one, since it would mean uploading data to a
server in order to read it back on the same machine that produced it.

## Decision

**IndexedDB is the permanent source of truth for the client. The server, when it
arrives in Phase 1, is a sync target — never the thing the UI reads from.**

Concretely:

- The full local workspace is stored in IndexedDB and hydrated into an in-memory
  observable store at boot. The UI queries the observable store.
- Mutations apply to the local observable synchronously, then enqueue a transaction
  for background sync. `capture.title = x; capture.save()` re-renders immediately;
  the network is not in the critical path.
- Navigation never fetches data that is already local.
- Rendering does not block on authentication. If a local store exists, paint it and
  let a stale session fail on the next sync delta.
- Rollback happens only on an explicit server rejection.

This is not a Phase 0 arrangement to be unwound later. It is the architecture for
every phase.

## Alternatives Considered

### Server-authoritative with a cache layer (TanStack Query / SWR)

- Pros: Conventional, well-understood, small amount of bespoke code. Optimistic
  updates are supported and would cover the mutable-field cases.
- Cons: The cache is a cache — it can be invalidated, and cold navigations still
  hit the network. Offline capture, which is a hard requirement for a tool used on
  flaky clinic wifi, would need a second parallel mechanism. And it does not
  address the fundamental awkwardness of uploading locally-produced data in order
  to read it back.
- Rejected: The offline requirement alone forces a durable local store. Having built
  one, treating it as a cache rather than the source of truth means maintaining two
  notions of truth and reconciling them.

### Local-first for Phase 0 only, migrate to server-authoritative in Phase 1

- Pros: Phase 0 ships without a server (see ADR-002) and the long-term architecture
  stays conventional.
- Cons: This is the worst of both. Every model, every read path, and every mutation
  path gets written twice, and the migration lands precisely when the product gains
  its first real users. The Phase 0 code becomes throwaway, which distorts how
  carefully it gets built.
- Rejected: Phase 0 having no server is a consequence of local-first, not a reason
  to treat local-first as temporary.

### Full CRDT-based replication (Yjs, Automerge)

- Pros: Principled conflict resolution, mature libraries, genuinely offline-first.
- Cons: Solves a problem this domain does not have. Capture timelines and assets are
  immutable — they cannot conflict. The mutable surface is four scalar fields plus
  append-only comments (see ADR-012).
- Rejected: A CRDT here is sophistication with no conflict to resolve, paid for in
  bundle size, debugging difficulty, and a data model harder to inspect.

## Consequences

- **Video lives in IndexedDB.** A two-minute 1080p recording is 20–40 MB. This
  creates a storage-pressure problem that server-authoritative designs do not have,
  and it requires an explicit budget and eviction policy — see ADR-009. This is the
  real cost of the decision, and it is paid in full.
- **Capture IDs must be minted client-side**, since a capture is valid before any
  server knows about it. See ADR-003.
- **The sync engine is bespoke code we own and must test.** Property-based
  convergence tests are mandatory, not optional.
- **Offline is a normal path, not an edge case.** A week offline followed by a
  reconnect must work, and must be tested.
- **The observable store must be granular** (per-field observables) or local-first
  buys nothing — a coarse store re-renders whole lists on single-field deltas and
  feels no faster than a network fetch.
- Migrating away from this decision later would mean rewriting every read path in
  every client. It is expensive to reverse, which is why it is recorded here.
