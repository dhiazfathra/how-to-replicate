# ADR-003: Client-minted ULIDs for capture and event identity

## Status

Accepted

## Date

2026-08-04

## Context

ADR-001 makes the client the source of truth, and ADR-002 removes the server from
Phase 0 entirely. A capture must therefore be complete, valid, referenceable, and
exportable before any server has heard of it. Server-assigned identity is not
available at the moment identity is needed.

Beyond that, the event timeline (ADR-004) needs a total order across events arriving
from three concurrent sources — CDP network events, console messages, and DOM
interaction events. Wall-clock timestamps collide at millisecond resolution under
burst load, and clocks can jump backwards during NTP correction or DST transitions.
An identifier that sorts correctly on its own removes an entire class of ordering
bug.

## Decision

**All identifiers are ULIDs minted on the client.**

- `capture.id`, `event.id`, `mutation.id`, `comment.id` — all client-generated.
- Event ULIDs are generated **monotonically** within a capture, so lexicographic
  sort of `event.id` is a valid total order requiring no tiebreaker.
- The server accepts client IDs as primary keys. It assigns no identity of its own.
- The server does own `capture.sync.revision` — a monotonic sync counter, distinct
  from identity, used for delta pull ordering (see ADR-012).

Separately but for the same reason: **event timestamps are millisecond offsets from
`capture.epoch`**, which is anchored to `performance.timeOrigin`. Not wall-clock
times.

## Alternatives Considered

### Server-assigned sequential integers

- Pros: Compact, index-friendly, human-quotable in conversation ("capture 1423").
- Cons: Requires a round trip before a capture has identity, which contradicts
  ADR-001 and is impossible under ADR-002. Also leaks total capture volume to anyone
  who can see an ID.
- Rejected: Structurally incompatible with local-first.

### Client-generated UUIDv4

- Pros: Universally supported, `crypto.randomUUID()` is built in, zero dependency.
- Cons: No embedded ordering, so the event timeline needs a separate sort key and a
  tiebreaker for same-millisecond events. Random distribution also fragments
  B-tree index locality in Postgres once the Phase 1 mirror exists.
- Rejected: The timeline ordering problem is real and recurring; solving it in the
  identifier is cheaper than solving it at every read site.

### Client-generated UUIDv7

- Pros: Time-ordered like ULID, and a formal IETF standard.
- Cons: Functionally equivalent to ULID for our purposes. Runtime and library
  support was less uniform at the time of the decision, and ULID's Crockford
  base32 encoding is more readable in logs and URLs than UUID's hyphenated hex.
- Rejected on a narrow margin. If UUIDv7 support becomes universal this is a
  low-cost swap — both are 128-bit, time-ordered, and lexicographically sortable.
  Nothing else in the design depends on the encoding.

### Composite key of `(clientId, localSequence)`

- Pros: Guaranteed unique with no randomness, trivially ordered per client.
- Cons: Two columns to carry everywhere, awkward in URLs, and requires durable
  per-client sequence state that survives reinstall. Sequence resets after a
  browser-profile wipe cause silent collisions.
- Rejected: Durable sequence state is a worse problem than the one it solves.

## Consequences

- **The server must trust client-supplied primary keys.** A malicious or buggy
  client can attempt to write a colliding ID. Mitigation: writes are scoped to the
  authenticated workspace, and inserts are idempotent — a repeated `(id, workspace)`
  with identical content is a no-op, while a conflicting one is rejected and
  alerted. This is an accepted, bounded trust extension, and it is the direct price
  of local-first identity.
- **Mutation idempotency comes free.** Because a mutation carries a client ULID, a
  retry after an ambiguous network failure is always safe to replay. This is what
  makes the Phase 1 sync queue robust without transactional coordination.
- **Timestamps are not comparable across captures.** `event.t` is meaningful only
  relative to its own `capture.epoch`. Any cross-capture time analysis (Phase 3
  pattern detection) must convert via `createdAt` first, and that conversion inherits
  whatever clock skew the reporting machine had.
- **ULIDs embed creation time**, so an ID discloses roughly when a capture was made.
  Acceptable — capture timestamps are not confidential — but worth knowing before
  putting IDs in externally-shared URLs.
- Swapping to UUIDv7 later is a contained change; swapping to server-assigned
  identity is not, as it would contradict ADR-001.
