# ADR-009: Local storage budget, and refuse-to-record before auto-eviction

## Status

Accepted

## Date

2026-08-04

## Context

ADR-001 makes IndexedDB the source of truth, which means video lives there. A
two-minute 1080p WebM recording runs 20–40 MB. A QA engineer filing several captures
a day accumulates gigabytes.

Browser quota is generous — Chromium typically permits a large fraction of free disk
— but it is not guaranteed, and by default a browser may evict an origin's storage
under disk pressure without warning. For a cache that is a minor annoyance. For the
only copy of a bug report it is data loss.

The pressure differs sharply by phase, and this is what drives the decision:

- **Phase 0 has no server.** A capture in IndexedDB is the *only* copy that exists
  anywhere. Evicting it destroys it.
- **Phase 1 onward**, a synced capture exists server-side too, so the local copy is
  genuinely a cache and safe to evict.

The conventional local-first answer — LRU eviction — is therefore correct in Phase 1
and catastrophic in Phase 0.

## Decision

**An explicit storage budget, with eviction behaviour that differs by whether a
durable second copy exists.**

### Always

- Request `navigator.storage.persist()` before the first capture, so the browser
  does not evict the origin under disk pressure.
- Check `navigator.storage.estimate()` **before** recording starts. If projected
  capture size exceeds remaining quota, refuse to start and say why.
- Default cap: **2 GB or 40 captures per workspace**, whichever binds first. Both
  policy-overridable.
- Video persists as **chunked blob records**, not one large value — bounded
  transaction size, and resumable upload once Phase 1 exists.

### Phase 1 and later

LRU eviction over captures where `sync.lastPushedAt !== null`. Unsynced captures are
never evicted, regardless of age or cap pressure.

### Phase 0

**Nothing is auto-evicted.** At the cap, new captures are blocked with an
export-or-delete prompt naming the largest and oldest captures.

Silently deleting the only copy of a bug report is unacceptable. Refusing to record
is merely annoying — and it is annoying at a moment when the user is present, paying
attention, and able to act.

## Alternatives Considered

### LRU eviction from the start, uniform across phases

- Pros: One code path. Never blocks the user. Standard local-first practice.
- Cons: In Phase 0 it deletes the only copy of a capture. A QA engineer who records
  forty bugs on Friday and files them Monday loses the earliest ones with no warning
  and no recovery.
- Rejected: Silent, unrecoverable data loss. The one failure mode a bug-tracking tool
  cannot have.

### No cap — rely on browser quota alone

- Pros: Zero implementation. Maximum captures retained.
- Cons: Quota exhaustion surfaces as an opaque `QuotaExceededError` mid-capture,
  after the user has already recorded and after the redaction pass has run. And
  without `persist()`, the browser may evict under disk pressure anyway. The failure
  lands at the worst possible moment.
- Rejected: Failing before recording is strictly better than failing after.

### Stream captures to disk via the File System Access API instead of IndexedDB

- Pros: Effectively unbounded storage. No quota concerns. Files directly accessible
  to the user.
- Cons: Requires a user gesture granting directory access per session, which breaks
  one-click capture. Not available in extension service worker contexts. Splits the
  data model across two storage systems, with metadata in IndexedDB and blobs on
  disk — and reconciling them after a partial failure is its own problem.
- Rejected: Breaks the one-click requirement, which is the product's premise.

### Aggressive compression or lower default recording quality

- Pros: More captures per byte. Complementary rather than exclusive to a budget.
- Cons: Not a substitute — it moves the cap, it does not remove the need for one.
  And blur regions compress poorly, while over-compressed video defeats the purpose
  of recording a visual bug.
- Deferred: A reasonable Phase 0 tuning exercise (resolution and bitrate defaults),
  but it does not replace an explicit budget.

## Consequences

- **Phase 0 users will hit the cap and be blocked.** This is the deliberate cost.
  Mitigation: the export-or-delete prompt is specific, naming the largest and oldest
  captures with sizes, so clearing space takes one click rather than an
  investigation. Exported `.htr` bundles are the durable second copy Phase 0 lacks.
- **`persist()` may be denied.** Chromium grants it based on engagement heuristics;
  a freshly-installed extension may not qualify immediately. Behaviour when denied:
  warn in the UI, keep the cap, and treat every capture as at-risk until Phase 1
  sync exists. Worth surfacing rather than hiding — a user who knows their captures
  are evictable exports them.
- **Eviction unlocking in Phase 1 changes user-visible behaviour.** Captures start
  disappearing locally once synced. Needs a clear "synced, available on the server"
  affordance, or it reads as data loss even though it is not.
- **Chunked video complicates playback.** The player reassembles chunks, or streams
  them via a `MediaSource` — more code than handing a single blob to a `<video>`
  element. Paid deliberately, for bounded IndexedDB transactions and resumable
  Phase 1 upload.
- **The cap is per workspace, but quota is per origin.** Several workspaces on one
  machine can collectively exhaust quota while each is individually under cap. The
  pre-record `estimate()` check is what actually protects against this; the
  per-workspace cap is a fairness heuristic, not the real guard.
- Moving to LRU-always later would be a small change — but only ever after sync
  exists, and it should be gated on `lastPushedAt` regardless of phase, so the
  condition is written once and holds forever.
