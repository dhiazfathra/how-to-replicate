# ADR-011: Self-hosted S3-compatible object storage on Nutanix

## Status

Accepted

## Date

2026-08-04

## Applies from

Phase 1. Phase 0 stores all blobs in client IndexedDB and has no object storage
(ADR-002, ADR-009).

## Context

From Phase 1, captures sync to a server, which means video and screenshot blobs come
to rest on infrastructure we operate. PRD open question #1 asks whether that is
self-hosted or managed, given the ongoing Nutanix migration.

The data is the deciding factor. A capture recorded on a patient-record screen
contains PHI in its video frames, and redaction is pattern-based with known gaps
(ADR-005). Every stored recording must be treated as potentially containing patient
data, because some will.

Under Indonesian PDP Law and ISO 27001 that has consequences for where bytes may rest,
who may hold them, and what contractual instrument governs a third party who does.
A managed provider needs a data processing agreement, a documented transfer basis,
and a compliance review — and until those exist, customer-facing Recording Links
(ADR-013) cannot ship on top of it.

The counterweight is real: managed storage with a CDN gives better playback latency
and near-zero operational burden. Self-hosting means we own capacity planning,
durability, backup, and availability for a workload with an unusual profile — large
immutable blobs, write-once, read-occasionally, with retention-driven deletion.

## Decision

**Self-hosted S3-compatible object storage (MinIO or equivalent) on Nutanix
infrastructure. Playback via signed URLs served from our own domain. No external
CDN.**

- All blob access goes through time-limited signed URLs; no public buckets, ever.
- Server-side encryption at rest; TLS in transit.
- Services code against the **S3 API only**. No provider-specific extensions, so the
  endpoint stays a configuration value.

Retention deletion cannot be a single atomic transaction — S3 and Postgres are two
systems with no shared commit. So it is a **recoverable protocol with an explicit
completion condition**, not a one-step "purge together":

1. Mark the capture `deleting` in Postgres (a state, not a delete — the row still
   exists so a crash mid-deletion is resumable, not silently lost).
2. Delete every blob under the capture's key prefix from object storage. Object
   keys are per-capture (ADR-003's ULID), so this is a scoped prefix delete, not a
   scan.
3. Only after step 2 confirms — a positive delete acknowledgment for every listed
   object, not merely "the request didn't error" — delete the Postgres row.
4. A reconciliation sweep runs on a schedule: any capture stuck in `deleting` past a
   grace period retries from step 2 (idempotent — deleting an already-deleted key
   is a no-op); any object-storage key with no matching Postgres row (from a crash
   between steps 2 and 3, an interrupted delete, or a bug) is itself deleted and the
   orphan is alerted, not silently cleaned up.

**Retention is "complete" only when no row is in `deleting` past its grace period
and the reconciliation sweep finds no orphaned keys.** That is the auditable
condition — not "we ran the job."

That last constraint is what keeps this decision cheap to revisit. The compliance
argument is what makes self-hosting the default; coding to the S3 API is what means a
future move is a configuration change and a data migration rather than a rewrite.

## Alternatives Considered

### Managed object storage plus CDN

- Pros: Better playback latency, especially for the 13+ geographically distributed
  brand surfaces. No capacity planning, durability engineering, or backup
  operation. Elastic cost.
- Cons: PHI-adjacent recordings leave the perimeter. Needs a DPA and a documented
  transfer basis, and until those are in place it blocks customer-facing Recording
  Links — a Phase 2 deliverable. CDN edge caching of signed URLs also means copies of
  patient-visible video sitting in edge caches we do not control, with cache
  invalidation semantics that do not map onto a retention guarantee.
- Rejected: The edge-cache retention problem is the decisive one. Signed URLs limit
  access, but a cached object's lifetime is the CDN's business, and "delete on
  request" is not something an edge cache promises.

### Postgres large objects or `bytea`

- Pros: One storage system. Transactional consistency between metadata and blobs —
  which genuinely removes the orphaned-blob problem.
- Cons: Postgres is a poor fit for tens of gigabytes of write-once video. Bloats
  backups, lengthens restore times, and makes streaming playback awkward. Range
  requests over `bytea` are possible but unpleasant.
- Rejected: Wrong tool at this size. The consistency benefit is real and is instead
  addressed by the paired-purge job.

### Hybrid — self-host recent captures, tier older ones to managed storage

- Pros: Bounded local capacity, cheap long-term retention.
- Cons: Tiering to a managed provider has the same compliance profile as using one
  outright, just later. Two storage backends, a migration path between them, and two
  deletion paths that both must satisfy retention.
- Rejected: Does not avoid the compliance obligation, and doubles the deletion surface
  — where deletion correctness is the compliance-critical operation.

### Keep everything client-side permanently, never store blobs server-side

- Pros: Zero server-side PHI at rest. Trivially compliant.
- Cons: No share links, no no-login viewing, no team visibility, no Recording Links,
  no MCP asset access. Removes most of what Phases 1–3 exist to deliver.
- Rejected: This is Phase 0, and Phase 0 is deliberately temporary.

## Consequences

- **We own durability.** Replication factor, backup, and restore testing for the
  object store are now operational requirements, not a vendor's problem. Restore must
  be exercised, not assumed — an untested backup of the only copy of a capture is not
  a backup.
- **Playback latency is worse for distant brand surfaces** than a CDN would give.
  Byte-range requests for seeking are a safe mitigation. **A reverse-proxy cache
  inside the perimeter is not automatically safe** — it creates another copy of
  PHI-adjacent video that the retention protocol above does not touch, since the
  cache never sees the S3/Postgres delete. If a reverse-proxy cache is added, it
  must carry its own bounded TTL short enough that a purged capture's cached bytes
  expire on their own within the retention grace period, and cache eviction must be
  explicitly triggered as a fourth step in the deletion protocol, not left to TTL
  alone — an explicit invalidation call, logged in the same audit trail as the
  blob and metadata deletes. Until that invalidation step exists, no reverse-proxy
  caching of these blobs is permitted.
- **Retention deletion is a recoverable protocol, not an atomic purge.** See
  Decision above — a `deleting` state, ordered blob-then-metadata deletes, and a
  reconciliation sweep are what make the guarantee provable rather than assumed.
  An orphaned key found by the sweep is a compliance defect and gets alerted, not
  silently cleaned up without record.
- **Capacity planning is now a real activity.** A 40 MB average capture across 13+
  brand surfaces at meaningful adoption is terabytes per year. Retention windows are
  the primary lever, which makes the Phase 1 retention default (spec §22) a capacity
  decision as much as a compliance one.
- **Signed URL expiry needs to be short** — minutes, not hours — because a leaked URL
  is unauthenticated access to a patient-visible recording. Short expiry means the
  viewer must refresh URLs mid-session for long playback.
- **S3-API-only discipline must be enforced**, not merely intended. A single
  provider-specific call quietly converts a configuration change into a migration
  project. Worth a lint rule or an interface boundary.
- Revisiting this is a configuration change plus a data migration — genuinely
  reversible, provided the API discipline holds.
