# ADR-012: Sync via mutation queue and delta pull, last-write-wins per field

## Status

Accepted

## Date

2026-08-04

## Applies from

Phase 1.

## Context

Phase 1 introduces a server, and ADR-001 fixes its role: a sync target, never the
source of truth the UI reads from. That requires a sync protocol — how local changes
reach the server, how remote changes reach the client, and what happens when they
disagree.

Conflict resolution is usually the hard part of local-first, and it is where CRDTs
earn their complexity. But this domain has an unusual property worth stating plainly:

**Almost nothing here is mutable.**

- Event timelines are append-only and immutable (ADR-004).
- Assets are write-once immutable blobs.
- Comments are append-only.
- The replication document is generated once; regeneration creates a new document
  rather than editing one in place.

The entire mutable surface on a **capture** is four scalar fields — `title`,
`summary`, `tags`, `assignment`. Workspace and project **membership** — who
belongs to a workspace, who has access to a project — is a separate mutable
surface with different semantics: it is an access-control decision, not a
capture-content edit, and this protocol's per-field last-write-wins rule is
scoped to capture mutations only. Membership changes are RBAC writes against
`workspace`/`project` records, authorized by workspace-admin role rather than
capture ownership, ordered by the same server-received-timestamp rule for
consistency, and — because they are access-control decisions — every membership
change is audit-logged, not merely subject to last-write-wins silently. They do
not go through the `Mutation` schema below, which is capture-scoped.

Immutable data cannot conflict. Two clients that both append events to the same
capture produce a union, not a conflict. So the conflict resolution problem here is
not "merge two divergent document states"; it is "two people renamed the same capture
and one of them has to lose."

Offline is a first-class requirement, not an edge case. Clinic wifi is unreliable, and
a QA engineer may capture for a week before reconnecting.

## Decision

**Append-only mutation log pushed to the server, revision-based delta pull back,
last-write-wins per field. No CRDT.**

### Transport

ConnectRPC over HTTP for mutations and uploads; WebSocket for delta fan-out.
Consistent with existing stack conventions and with ConnectRPC for browser clients.

### Push

```ts
type Mutation =
  | { id: string; captureId: string; op: 'set'; field: 'title' | 'summary' | 'tags';
      value: string | string[]; clientTs: string }
  | { id: string; captureId: string; op: 'assign'; field: 'assignment';
      value: UserRef | null; clientTs: string }
  | { id: string; captureId: string; op: 'append'; field: 'comments';
      value: CommentInput; clientTs: string };
```

A **closed** field/operation set, not `field: string`. `set` and `assign` are
scalar-replacement, validated server-side against the field's actual type before
being applied — a client cannot target an event, an asset, or any field this
protocol doesn't name. `append` is its own operation kind, not a `set` on a
`comments` field with array-replacement semantics — comments are append-only
(ADR-004 §Context), so representing them as a scalar `set` would let a buggy or
malicious client silently replace the whole comment history rather than add to
it.

Mutations append to a local queue in IndexedDB, batched and flushed by the sync
engine. **Idempotent on `mutation.id`** — a replay after an ambiguous network failure
is always safe, which is what removes the need for transactional coordination
between client and server. The queue survives restart.

### Pull

`since=<revision>` delta stream. The server owns revision numbering — it is a sync
counter, distinct from identity (ADR-003), and it is the one thing the server is
authoritative about.

### Conflict resolution

Last-write-wins per field, ordered by **`(server-received timestamp, revision,
mutation.id)`** — a tuple, not the raw timestamp alone. A bare timestamp does not
define a total order: two mutations can arrive within the same clock tick, and a
multi-replica server has no single clock to receive against in the first place.
`revision` is the server's own monotonic counter (see Pull, above) and breaks any
timestamp tie deterministically; `mutation.id`'s ULID (ADR-003) breaks the
vanishingly rare case of two revisions landing identically. **Retries preserve the
first-accepted ordering** — a redelivered mutation is resolved against the
revision it was originally assigned, not re-raced against current state — so every
client and every server replica computes the same winner from the same inputs.

Immutable data unions. Comments append. Only the four scalar fields can ever
conflict, and for those the loser's value is recorded in the audit log so a
surprised user can find out what happened.

### Assets

Presigned PUT direct to object storage (ADR-011), but the presigned URL is not a
blank check: the server selects the object key (write-once, scoped to the
capture's ULID — never client-supplied, closing off key collision or overwrite),
and the manifest entry for each asset carries the expected size and content hash
before the PUT is issued. On upload, the server verifies the stored object against
both before accepting it into the manifest. A capture reaches `synced` only when
**every** asset in its manifest has been verified this way — a capture whose
metadata synced but whose video is still uploading, unverified, or size/hash-mismatched
is not synced, must not be eligible for local eviction (ADR-009), and the
non-public access model of ADR-011 (signed URLs, no public buckets) applies to
these presigned PUTs exactly as it does to reads.

### UI contract

```ts
capture.title = 'Checkout fails on Safari';
capture.save();
```

First line writes the local observable, re-rendering synchronously. Second line
enqueues. Rollback occurs only on explicit server rejection.

## Alternatives Considered

### CRDT-based replication (Yjs, Automerge)

- Pros: Principled, well-tested, genuinely handles concurrent editing. Removes
  last-write-wins data loss entirely.
- Cons: Solves a problem we do not have. There is no concurrent rich-text editing
  here; there are four scalar fields. The cost is bundle size, a data model that is
  hard to inspect in a debugger, tombstone growth requiring compaction, and
  server-side storage of CRDT internals alongside the plain values that every other
  consumer — Postgres queries, MCP export, tracker payloads — actually needs.
- Rejected: Sophistication with no conflict to resolve. If rich collaborative editing
  of capture documents ever becomes a requirement, revisit for that field alone.

### Full-state push — client sends the whole capture on every change

- Pros: Simplest possible protocol. No mutation log, no field-level reasoning.
- Cons: A capture with its event timeline is megabytes; pushing all of it to rename it
  is absurd. Worse, it makes concurrent changes to *different* fields conflict with
  each other, manufacturing conflicts the mutation log avoids entirely.
- Rejected: Creates the conflicts it then has to resolve.

### Server-authoritative with optimistic UI and rollback

- Pros: Conventional. No sync engine to own. Well-supported by existing libraries.
- Cons: Contradicts ADR-001. Does not survive a week offline — optimistic updates are
  a latency-hiding technique, not a durability mechanism.
- Rejected: Offline is a requirement, not a nicety.

### Operational transformation

- Pros: Mature, proven in collaborative editors.
- Cons: Requires a central sequencing authority and careful per-operation transform
  functions. All the complexity of CRDTs plus a coordination requirement, for the same
  non-existent conflict.
- Rejected: Same reason as CRDTs, with additional coupling.

### Client-timestamp last-write-wins

- Pros: Simpler — no server clock involvement, and offline changes keep their
  original ordering.
- Cons: A client with a fast clock wins every conflict, permanently and invisibly.
  Clock skew becomes a silent authority gradient.
- Rejected: Server-received timestamp costs nothing and removes the whole class of
  problem.

## Consequences

- **Immutability is doing the work, not the sync engine.** The reason this protocol is
  simple enough to own confidently is ADR-004's append-only model. That dependency is
  worth naming: if the timeline ever becomes mutable, this decision must be revisited
  from scratch.
- **Last-write-wins loses data, by design.** Two simultaneous renames means one is
  discarded. Acceptable for a title; it would not be for capture content — which is
  precisely why capture content is immutable. The discarded value goes to the audit
  log so it is recoverable by a human.
- **Property-based convergence tests are mandatory.** Random mutation interleavings
  across simulated clients must converge to one state. This is the test that catches
  the class of bug a sync engine actually has, and it cannot be replaced by
  example-based tests.
- **The offline queue has a defined cap: 5 000 mutations or 5 MB, whichever binds
  first**, shared against the same per-workspace storage budget as captures
  (ADR-009) rather than a separate unbounded allocation — the queue is small scalar
  writes, so this cap is generous relative to how a workspace could plausibly
  fill it through normal use. At the cap, new local edits still apply to the
  observable store (the UI never blocks); what happens to the pending mutation
  differs by operation, because a bare "stop enqueueing" would silently strand
  edits with nothing to sync once reconnected:
  - `set` and `assign` **coalesce**: a second edit to a field that already has a
    queued mutation overwrites that mutation's `value`/`clientTs` in place rather
    than appending a new entry. This is safe because these fields are
    last-write-wins scalars anyway (§Conflict resolution) — only the newest value
    for a given field is ever going to win, so there is nothing lost by
    collapsing the queue to just that value before it even reaches the server.
    Coalescing is what keeps the queue bounded under repeated edits to the same
    few fields, which is the common case (re-titling a capture several times
    before reconnecting).
  - `append` (comments) **cannot** coalesce — collapsing two comments would
    discard one. So a comment made at the cap is queued only if it fits within
    the cap; if it doesn't, the UI blocks *new comments specifically* (not other
    edits) with an explicit "sync queue full — reconnect to add more comments"
    message, rather than accepting the comment and then silently failing to send
    it.
  - The UI shows a persistent "N changes waiting to sync" indicator throughout.
    The user's recourse is reconnecting, not losing work — a full queue is a
    signal to get back online, not a data-loss event.
- **Partial sync is a real state.** Metadata synced, assets pending. The UI must
  distinguish "synced" from "synced except the video", and eviction (ADR-009) must
  respect the difference or it will delete an unbacked video.
- **Server revision numbering is a single-writer bottleneck** per workspace. Fine at
  expected volume; if it ever binds, per-workspace sharding is the escape hatch, since
  revisions never need to be globally ordered.
- **WebSocket fan-out needs a reconnect-with-backoff and a resync-from-revision path.**
  A dropped socket must never mean a missed delta — the `since` parameter is what makes
  recovery a normal operation rather than a repair.
