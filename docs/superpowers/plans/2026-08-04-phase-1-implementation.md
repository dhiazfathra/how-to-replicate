# Phase 1 Implementation Plan — local-first sync

**Derives from:** [design spec](../specs/2026-08-04-how-to-replicate-design.md) §10, §11, §19 "Phase 1 — local-first sync"
**Depends on:** [Phase 0 plan](2026-08-04-phase-0-implementation.md) complete and merged
**Date:** 2026-08-04

Phase 1 is where the first server we operate appears. Everything about it is
shaped by one constraint carried forward from Phase 0: **the server is a sync
target, never the thing the UI reads from** ([ADR-001](../../decisions/ADR-001-local-first-architecture.md)).

Phase 2 and 3 work (`router`, `summarizer`, `mcp-gateway`, webhooks, CLI,
server-backed Recording Links, iOS) is out of scope for this plan.

---

## Global Constraints

### Invariants — the Phase 0 five, still binding

1. No unredacted capture is viewable, exportable, or routable. The gate is
   `state === 'ready'`.
2. No unblurred frame exists in a persisted artifact.
3. LLM steps cite event IDs that exist, or they are dropped.
4. Lost fidelity is visible — `fidelity: 'degraded'`, `withheldEventCount`.
5. `packages/capture-core` depends on nothing in `clients/`.

### Invariants added by Phase 1

- **6. The server never becomes the read path.** No client code introduced in
  this phase may fetch data the local store already holds. A component that
  renders from a server response instead of the observable store is a defect,
  not a preference.
- **7. `redaction-audit` is an alarm, not a filter.** By the time data reaches
  it, unredacted content has already crossed the network boundary and the
  incident has already occurred. It must never be written or described as a
  redaction step. (The blocking-gate role exists only for the Phase 2 anonymous
  upload path, which is not in this plan.)
- **8. Eviction requires `sync.manifestComplete === true`**, not `lastPushedAt`.
  A capture whose video is still uploading is never evictable, however stale its
  metadata push looks.
- **9. Nothing unredacted is transmitted.** Redaction completed client-side in
  Phase 0 before the write to IndexedDB; sync uploads what is already redacted.
  No Phase 1 code path may transmit an event that bypassed the redactor.

### Conventions

- **Local-first.** Mutations apply locally and synchronously, then enqueue.
  Rollback happens **only on explicit server reject** — never on timeout, never
  optimistically-pessimistically on a slow response.
- **Event timestamps are ms offsets from `capture.epoch`.** The server stores
  them as offsets too; it does not normalize them to wall-clock.
- **Client-minted ULIDs.** The server assigns **revision numbers** and nothing
  else. It never mints an entity ID.
- **Immutability.** Timelines, events, and assets are immutable. There is no
  update or delete path for an event — retention purges the whole capture.
- **No CRDT.** The mutable surface is `title`, `summary`, `tags`, `assignment`,
  plus append-only comments. Last-write-wins per field is sufficient and is the
  decision ([ADR-012](../../decisions/ADR-012-sync-protocol.md)). Do not
  introduce one.
- **Animate only `transform` and `opacity`.**

### Engineering standards

- **100% coverage of branches and edge cases**, TypeScript and Go alike.
- **Lint clean:** `pnpm lint` for TS, `golangci-lint run` for Go. Both exit 0
  before a task is DONE.
- Every task ends with a real commit, conventional message.
- No new runtime dependency beyond the ones this plan names. If a task believes
  it needs one, report `DONE_WITH_CONCERNS` and say why.
- Update the README for anything that changes setup or usage.

### Stack decisions (already made — do not re-decide)

| Concern | Decision |
|---|---|
| Language | Go for services; pin the toolchain in `go.work` and every `go.mod` |
| Workspace | `go.work` at repo root covering `services/*` |
| HTTP | `chi` router + `otelhttp` middleware (spec §11) |
| RPC | ConnectRPC (`connectrpc.com/connect`) for browser clients; gRPC internally |
| Proto | Buf (`buf.yaml`, `buf.gen.yaml`), generated code checked in |
| Postgres driver | `pgx/v5` |
| Queries | `sqlc` — generated type-safe Go from SQL. No ORM |
| Migrations | `goose`, plain SQL, forward-only in CI |
| Object storage | S3-compatible, `minio-go` client; MinIO in tests |
| Auth | OIDC against the existing IdP via `coreos/go-oidc` |
| Realtime | WebSocket for delta fan-out (`coder/websocket`) |
| Testing | Table-driven units; `testcontainers-go` for Postgres + MinIO |
| Lint | `golangci-lint` |
| CI | GitHub Actions, existing workflow gains Go jobs |

### Repository layout added by this phase

```text
services/
  sync-gateway/       mutation intake, revision assignment, delta fan-out
  capture-api/        reads, share links, comments, workspace RBAC
  redaction-audit/    verification pass — alarm, not filter
  internal/           shared Go packages (db, auth, otel, storage, outbox)
proto/                buf module: sync.v1, capture.v1
packages/
  sdk/                JS SDK with metadata()
```

---

## Task 1: Carried-over Phase 0 verification hardening

Phase 0 deferred two verification assets. They land before the server does,
because the server multiplies the cost of a redaction hole.

**Deliverables**

- **Playwright extension E2E** — `clients/extension/e2e/`. Playwright persistent
  context loading the unpacked extension (spec §18). Cover the happy path
  (arm → capture → redact → ready → export), the off-allow-list refusal, and the
  CDP-detach → `fidelity: 'degraded'` handoff. Runs in CI on every PR.
- **Nightly video-blur OCR job** — `.github/workflows/nightly-blur.yml`. A fixture
  page renders known synthetic PHI text; capture it, decode frames, OCR-assert no
  match (spec §18). Slow, so it is a nightly job — the non-video corpus keeps
  gating every PR. A match fails the job loudly; do not downgrade it to a warning.
- Both jobs blocking in their respective workflows. Neither may be marked
  `continue-on-error`.

**Verification:** both suites green locally and in CI; the blur job proven to
fail by temporarily feeding it an unblurred fixture, then restored.

---

## Task 2: Go workspace scaffold, shared internals, and CI

The foundation every service builds inside. No business logic.

**Deliverables**

- `go.work` at repo root covering `services/*`; one `go.mod` per service plus
  `services/internal`.
- `proto/` as a Buf module with `buf.yaml`, `buf.gen.yaml`, and lint/breaking
  configuration. Generated Go checked in; CI fails if regeneration produces a
  diff.
- `services/internal/` shared packages, each independently testable:
  - `db` — `pgx/v5` pool construction, health check, transaction helper.
  - `migrate` — `goose` runner, embedded migrations.
  - `otel` — tracer/meter setup and the `otelhttp` middleware wiring.
  - `httpx` — `chi` router construction with the standard middleware stack
    (request ID, recovery, otelhttp, structured logging).
  - `storage` — S3-compatible object client over `minio-go`: `PresignPut`,
    `Stat`, `Delete`. Bucket and key policy live here, not in callers.
  - `authz` — workspace-scoped RBAC primitives: role model, permission checks.
    Pure functions; no I/O.
- `services/internal/testsupport` — `testcontainers-go` harnesses spinning
  Postgres and MinIO, shared by every service's integration tests.
- `sqlc.yaml` and `goose` wiring so `sqlc generate` and migration runs are one
  command each.
- `.golangci.yml` — enable at minimum `errcheck`, `govet`, `staticcheck`,
  `revive`, `gosec`, `bodyclose`, `rowserrcheck`, `sqlclosecheck`.
- CI: add `go-lint`, `go-test` (with coverage, 100% threshold), and `buf-check`
  jobs to `.github/workflows/ci.yml`. All blocking.
- A `Makefile` or `taskfile` is **not** wanted; pnpm scripts and `go` commands
  are enough. Do not add one.

**Verification:** `go work sync && golangci-lint run ./... && go test ./... && buf lint && buf breaking` all clean on an otherwise empty service set.

---

## Task 3: Schema, protos, and the closed mutation vocabulary

The wire contract and the database that mirrors the local schema. Get this wrong
and every later task inherits the mistake.

**Deliverables**

- **Postgres schema** (`services/internal/db/migrations/`), mirroring the local
  schema from Phase 0 Task 2 and Task 5:
  - `workspaces`, `projects`, `users`, `memberships` (role per workspace).
  - `captures` — client ULID as primary key, `workspace_id`, `project_id`,
    `source`, `state`, `fidelity`, `created_at`, `epoch`, `env` (jsonb),
    `metadata` (jsonb), `doc` (jsonb), `withheld_event_count`, `revision`,
    `manifest_complete`, and **`applied_ruleset_version`** — the ruleset the
    *client* actually redacted with. `redaction-audit` (Task 12) evaluates
    against whatever ruleset is current when it runs, so without this column a
    finding after a ruleset update is unattributable: nobody can tell a genuine
    client-side hole from a rule that did not exist yet. The audit's own
    evaluation version is recorded separately, on the finding, not here.
  - `capture_events` — append-only. `id` (client ULID) primary key, `capture_id`,
    `t` (ms offset — **not** a timestamp column), `kind`, `payload` (jsonb),
    `redaction` (jsonb). A `REVOKE UPDATE, DELETE` grant plus a trigger that
    raises on either. Immutability enforced by the database, not by convention.
  - `assets` — `id`, `capture_id`, `kind`, `mime_type`, `size_bytes`,
    `chunk_count`, `sha256`, `object_key`, `verified_at`.
  - `mutations` — append-only intake log, primary key on the **client-minted
    mutation ULID** so replay is idempotent at the database level.
  - `comments` — append-only.
  - `redaction_rulesets` — versioned, replaced wholesale, never edited.
  - `audit_log` — append-only, same immutability enforcement as `capture_events`.
  - `share_links`, `integration_bindings`, `outbox` (created here, used in
    Phase 2).
- **Protos** in `proto/sync/v1/` and `proto/capture/v1/`:
  - The mutation type is a **closed field/operation set**, never a bare
    `field: string`:

    ```proto
    message Mutation {
      string id = 1;             // client-minted ULID
      string capture_id = 2;
      oneof op {
        SetTitle set_title = 10;
        SetSummary set_summary = 11;
        SetTags set_tags = 12;
        Assign assign = 13;
        AppendComment append_comment = 14;
      }
      int64 client_t = 20;       // ms offset, client clock — advisory only
    }
    ```

    Only `set`, `assign`, and `append` operations exist. Adding a new mutable
    field means adding a message here, deliberately, with a migration.

  - `SyncService`: `PushMutations`, `PullDeltas` (`since=<revision>`),
    `RequestAssetUpload`, `CompleteAssetUpload`.
  - `CaptureService`: `GetCapture`, `ListCaptures`, `ListEvents`, `CreateComment`,
    `CreateShareLink`, `RevokeShareLink`.
- `sqlc` queries and generated code for every table above.
- **Two database roles, not one.** A `REVOKE UPDATE, DELETE` grant does nothing
  if the service connects as the table owner, and owners bypass RLS besides. So:
  a **migration role** that owns the schema and runs `goose`, and a **runtime
  role** that every service connects as, holding `SELECT` and `INSERT` on the
  append-only tables and no `UPDATE` or `DELETE` at all. The triggers stay as
  defence in depth — they catch the owner too — but the grant is the primary
  control, and it only means something against a role that is not the owner.

**Tests:** migration up/down against a testcontainer Postgres; the immutability
triggers proven by asserting an UPDATE and a DELETE on `capture_events` both
error **when connected as the runtime role**, since that is the only connection
the services ever make; the same two statements also rejected as the migration
role, proving the trigger and not just the grant is doing work;
`sqlc`-generated round-trips for every table.

---

## Task 4: sync-gateway — mutation intake, revisions, and last-write-wins

Spec §10 and [ADR-012](../../decisions/ADR-012-sync-protocol.md).

**Deliverables** in `services/sync-gateway/`:

- ConnectRPC handler for `PushMutations`. Batched intake.
- **Idempotency on the mutation ULID.** A retry after an ambiguous failure is
  always safe: re-pushing a mutation the server already applied returns the same
  result and changes nothing. Implement this as an insert on the `mutations`
  primary key, not an application-level "have I seen this" cache.
- **The server owns revision numbering.** Each accepted mutation batch advances
  the capture's `revision`. Clients never assign one.
- **Conflict resolution on the tuple `(server-received timestamp, revision, mutation.id)`**,
  in that order — not a bare timestamp. Equal timestamps break on revision, equal
  revisions break on the mutation ULID. Deterministic for any interleaving.
- Last-write-wins **per field**. Two mutations touching different fields both
  apply; two touching the same field resolve by the tuple.
- `append` operations (comments) never conflict — they append.
- Rejection is explicit and typed: a rejected mutation returns a reason the client
  can act on. Silence or a generic error is not acceptable, because the client's
  rollback rule keys on explicit reject.
- Auth: every RPC is workspace-scoped through `internal/authz`. A mutation naming
  a capture outside the caller's workspace is rejected, and the rejection does not
  leak whether the capture exists.

**Tests:** table-driven per operation; idempotent replay of a full batch;
conflict resolution across every tie-break level of the tuple; cross-workspace
rejection; a mutation for an unknown capture.

---

## Task 5: sync-gateway — asset upload and manifest verification

The half of sync where "synced" actually gets defined.

**Deliverables**

- `RequestAssetUpload` returns a **presigned PUT** against a **server-selected**
  object key. The client never chooses the key.
- **A server-selected key is not by itself write-once.** A presigned URL stays
  usable until it expires, so the same URL can overwrite the object it just
  created. Close that properly rather than by naming convention:
  - Presign with a **short expiry** (minutes, matched to the asset size) and
    treat each presign as **single-use** — record its issuance and refuse to
    verify an object whose key was presigned more than once.
  - Presign with the checksum the client declared bound into the request
    (`x-amz-checksum-sha256`), so the store itself rejects a body that does not
    match, and add `If-None-Match: *` where the backend honours it so a second
    PUT to a live key fails at the store.
  - A second upload request for the same asset yields a **new key**, never a
    reuse; the old key is orphaned and swept.
- Upload goes **straight to object storage**, not through the service.
- `CompleteAssetUpload` triggers verification against **size and hash**. The
  hash must be one the server can trust: either the store's own checksum for an
  upload whose checksum was bound at presign time, or — when the backend cannot
  provide that — a **server-side read-and-hash of the object**. A bare `Stat`
  or `ObjectAttributes` value that the client could have influenced is not
  evidence, and `verified_at` must never be set from one.
- **`sync.manifestComplete` flips to `true` only once every asset in the manifest
  has verified.** Partial verification leaves it false. This flag — not
  `lastPushedAt` — is what the client's eviction rule reads (invariant 8).
- A verification mismatch marks the asset unverified, leaves `manifestComplete`
  false, and surfaces a typed error. It never deletes the client's copy and never
  silently accepts.
- Object storage is self-hosted S3-compatible on Nutanix
  ([ADR-011](../../decisions/ADR-011-self-hosted-object-storage.md)): server-side
  encryption on, playback strictly via signed URLs, **no external CDN**, no public
  bucket policy. Assert the bucket policy in a test.

**Tests:** MinIO testcontainer end to end — request, PUT, complete, verify;
size mismatch and hash mismatch each leave `manifestComplete` false; **a second
PUT to an already-uploaded key is rejected, and the object is unchanged after
it**; a presigned URL replayed after verification does not flip
`manifestComplete`; an expired presign is refused; a re-request for an
already-verified asset does not produce an overwriting key; the bucket rejects
anonymous reads.

---

## Task 6: sync-gateway — delta pull and WebSocket fan-out

**Deliverables**

- `PullDeltas(since=<revision>)` streaming every change above the given revision,
  ordered by revision. The response is a delta, not a snapshot.
- WebSocket endpoint for delta fan-out: a connected client subscribed to a
  workspace receives deltas as they are assigned. The socket is a **latency
  optimization over the pull**, never the only path — a client that missed
  messages while disconnected recovers entirely through `PullDeltas`. Test that
  explicitly.
- Backpressure: a slow consumer is disconnected rather than buffered without
  bound, and reconnects into the pull path.
- Fan-out is workspace-scoped through `internal/authz`; a socket never receives a
  delta from a workspace the connection is not authorized for.
- **Authorization is revalidated for the life of the socket, not just at the
  handshake.** A long-lived connection outlives the decision that opened it: a
  membership can be removed, a role downgraded, a token expired, or workspace
  access revoked while the socket keeps streaming. Re-check authorization before
  each delta batch (cheap — it is a cached membership lookup) and **close the
  socket** when the check fails or the token's expiry passes. A revoked user
  reading deltas over an open socket is the same disclosure as a revoked user
  reading the API, and it lasts as long as the connection does.

**Tests:** a client that disconnects, misses N revisions, reconnects, and pulls
converges to the same state as one that stayed connected; slow-consumer
disconnect; cross-workspace isolation; **membership removal, role downgrade,
token expiry, and workspace revocation each close an already-open socket before
the next delta reaches it**.

---

## Task 7: capture-core — the client sync engine

The client half of §10. Lives in `packages/capture-core/src/sync/`, so all three
clients get it. Invariant 5 still binds: no imports from `clients/`.

**Deliverables**

- `queue.ts` — the mutation queue in IndexedDB. **Survives restart.** Capped at
  **5 000 mutations / 5 MB**.
  **Capacity is checked before the local mutation is applied, not after.** An
  indicator does not prevent loss: if `capture.save()` writes the observable and
  then finds the queue full, the user sees their edit, closes the tab, and it is
  gone. So `save()` consults the queue first and **rejects the edit** when there
  is no room, leaving the previous value in place and surfacing why. Refusing an
  edit is annoying; accepting one that silently evaporates is the same class of
  failure as silently deleting a capture.
  The "pending sync" indicator shows queue depth as it approaches the cap, so the
  refusal is never the user's first warning.
- `engine.ts` — `createSyncEngine({ transport, repo, store, queue })`:
  - Batch and flush pushes; exponential backoff with jitter on failure.
  - Apply pulled deltas into the local store, advancing the known revision.
  - **Rollback only on explicit server reject.** A timeout, a network error, or a
    disconnected socket all mean "retry later", never "undo the user's edit".
  - **Rollback is conditional on the rejected mutation still being the latest for
    that field.** Rejections arrive late and out of order; restoring a
    pre-mutation value wholesale would silently revert every newer edit the user
    made while the reject was in flight. Track a per-field version (or the
    ordered mutation IDs per field): if newer pending mutations exist for the
    field, roll back to the rejected mutation's base and **replay** them, so the
    user's most recent intent survives. If it is the latest, plain rollback.
  - Resume cleanly after a week offline. That is a normal, tested path — not an
    edge case.
- `mutations.ts` — the closed operation set mirrored from the proto: `setTitle`,
  `setSummary`, `setTags`, `assign`, `appendComment`. No generic setter.
- **UI contract:** `capture.title = x; capture.save()` writes the local
  observable (synchronous re-render) and enqueues. Implement exactly that shape;
  the viewer depends on it.
- `transport.ts` — the ConnectRPC client behind an interface so tests inject a
  fake and `capture-core` stays transport-agnostic.
- The engine is **inert when no transport is configured**, so a Phase 0 build
  keeps working unchanged.

**Tests:** queue persistence across a simulated restart; **a `save()` at the cap
is refused and the observable still holds the previous value after a simulated
restart**; the indicator reflects depth; the rollback rule proven for reject vs.
timeout separately; **an out-of-order reject for an older mutation does not
clobber a newer local edit** (reject arrives after two further edits to the same
field); a week-offline replay; the inert-without-transport path.

---

## Task 8: Property test — mutation interleavings converge

Spec §18 names this specifically. It is its own task because it is the thing that
proves the LWW decision was sound.

**Deliverables**

- `packages/capture-core/test/sync-convergence.property.test.ts` — generate
  random mutation sets across multiple simulated clients, apply them in random
  interleavings and with random delivery delays and duplicates, and assert every
  client converges to **one identical state**.
- The generator covers each operation in the closed set, concurrent same-field
  writes, concurrent different-field writes, duplicate delivery (idempotency),
  and out-of-order delivery.
- Failures must print a **minimal reproducing interleaving**, not just "property
  violated" — a shrinker or a hand-rolled equivalent.
- Runs against the real client engine and a faithful in-process model of the
  server's tuple ordering, so a divergence between client and server rules is
  caught here rather than in production.

**Constraint:** if this test fails, the sync rules are wrong. Do not weaken the
property, reduce the iteration count below a meaningful level, or mark it flaky.

---

## Task 9: Local storage — LRU eviction unlocks

Spec §9. Phase 0 evicted nothing; Phase 1 unlocks eviction under one condition.

**Deliverables**

- `packages/capture-core/src/storage/eviction.ts` — LRU over captures where
  `sync.manifestComplete === true` **only**. A capture whose video is still
  uploading is not eligible no matter how stale `lastPushedAt` looks
  (invariant 8).
- Eviction runs when the budget check from Phase 0 Task 5 reports pressure. If
  nothing is eligible, the result is still **refuse-to-record** with the
  export-or-delete prompt — never "evict the least bad unsynced capture".
- Eviction removes local assets and events, keeps the capture metadata record
  with a `localAssets: false` marker so the viewer can offer to re-fetch from the
  server rather than showing a broken capture.
- The Phase 0 refusal path stays intact for the no-transport build.

**Tests:** an unsynced capture is never evicted even as the sole candidate; a
`lastPushedAt`-recent but `manifestComplete: false` capture is not evicted; LRU
ordering; the all-ineligible case falls back to refusal.

---

## Task 10: Identity — SSO, workspaces, projects, RBAC

**Deliverables**

- `services/internal/auth` — OIDC against the existing IdP (`coreos/go-oidc`):
  authorization-code flow for the web viewer, token verification middleware for
  the services. No password handling of our own, ever.
  `go-oidc` verifies the token; it does not run the flow's anti-forgery controls
  for you, so state them as requirements rather than assuming the library covers
  them:
  - **PKCE** (S256) on the authorization-code flow, as with the Phase 0 GitLab
    tracker. The public client has no secret to fall back on.
  - **`state`** generated per attempt, stored against the session, and compared
    on callback — the CSRF control.
  - **`nonce`** generated per attempt and asserted to match the claim in the
    returned ID token — the replay control. Verifying signature, `iss`, `aud`,
    and expiry without `nonce` still accepts a replayed token.
  - JWKS fetched with caching and **key rotation honoured**; an unknown `kid`
    triggers a refetch rather than a hard failure, and a token whose key is gone
    after refetch is rejected.
- Workspace and project CRUD in `capture-api`. Projects map to **brand surfaces**
  and carry the `projectId` that Phase 0's label derivation already consumes.
- Workspace-scoped RBAC using `internal/authz`: roles `owner`, `member`,
  `viewer`. Every read and every mutation checks. Cross-workspace access returns
  a not-found that does not disclose existence.
- **Render-first-authenticate-second** in the client: if a local store exists,
  paint immediately and let a stale session fail on the next sync delta. Never
  gate first paint on a token check. This is the spec §11 decision and it stands.
  What it must not become is painting *someone else's* workspace: on a shared
  machine, logging out and back in as a different user would otherwise show the
  previous user's captures for as long as it takes the first delta to fail. So
  the local store is **partitioned by `(subject, workspaceId)`**, and first paint
  reads only the partition for the **last authenticated identity**, recorded
  locally at login. On logout, or when the identity on the next successful auth
  differs from the recorded one, the prior partition is **closed and purged**
  before anything from it renders.
  The distinction that matters: render-first trusts a *stale* session, which is
  the user's own. It never trusts a *different* one.
- Per-workspace policy object carrying the Phase 0 knobs that were already
  described as policy-overridable: storage cap, capture cap, LLM provider chain
  and its `localOnly` flag, origin allow-list, retention window.

**Tests:** the RBAC matrix exhaustively (role × operation); the non-disclosing
cross-workspace response; token expiry mid-session leaving the painted UI intact
until the next delta; **logout followed by login as a different subject renders
nothing from the first subject's partition at any point, including first paint**;
policy resolution and defaults. For OIDC: a mismatched `state` and a mismatched
`nonce` are each rejected; a replayed ID token is rejected; an unknown `kid`
triggers exactly one JWKS refetch and then succeeds or rejects.

---

## Task 11: capture-api — reads, comments, and share links

**Deliverables** in `services/capture-api/`:

- `GetCapture`, `ListCaptures`, `ListEvents` — reads for share-link viewers and
  for a client whose local copy was evicted. **Not** a read path the local-first
  UI uses when data is already local (invariant 6).
  **Every one of these enforces the ready gate**, not just the share-link
  resolver. Invariant 1 says no unredacted capture is viewable, and an
  authenticated `GetCapture` returns exactly as much content as a share link
  does. For a capture in `recording`, `redacting`, `composing`, `failed`, or
  `expired`, these return **status metadata only** — id, state, `fidelity`,
  `createdAt` — and no `doc`, no events, no assets, no `metadata`. Enforce it in
  one place both the resolver and the handlers route through, so a future
  endpoint cannot forget.
- `CreateComment` — append-only, workspace-scoped.
- **Share links:** signed, expiring, revocable, no-login viewing.
  - Signed with a rotating server key; the token carries capture ID, expiry, and
    a revocation ID — not a bare capture ID.
  - Revocation is immediate and checked on every resolution, so revoking is not
    dependent on the expiry.
  - **A share link resolves only for a capture in `state === 'ready'`**
    (invariant 1). Enforce it in the resolver, not the caller.
  - Every share-link resolution writes an `audit_log` entry.
- Rate-limit share-link resolution so a token cannot be brute-forced.

**Tests:** an expired token, a revoked token, a token for a non-`ready` capture,
and a forged signature each fail; a valid token succeeds and writes exactly one
audit entry; rate limiting trips; **`GetCapture`, `ListCaptures`, and
`ListEvents` each return status metadata only for a capture in every non-`ready`
state**, asserted field by field rather than on the response being non-empty.

---

## Task 12: redaction-audit — the alarm

Spec §11. Read invariant 7 before writing a line of this.

**Deliverables** in `services/redaction-audit/`:

- Consumes newly-synced captures, re-runs the current ruleset server-side over
  their events and metadata, and **alerts on any finding** — pages Security,
  records the finding, and marks the capture for review.
- It **does not** redact, mask, quarantine, or modify anything. By the time data
  reaches it, unredacted content has already crossed the network boundary. Its
  value is detecting ruleset gaps, and a filter here would hide exactly the
  signal it exists to produce.
- The ruleset it runs is the same versioned bundle format the client uses. Port
  or bind the Phase 0 engine's semantics faithfully; a divergence between the two
  implementations produces false alarms that get ignored, which is worse than no
  alarm. Share the rule semantics through a conformance test suite driven by the
  **same golden corpus fixtures** as Phase 0 Task 4, so both implementations are
  proven to agree.
- Findings write to `audit_log` and to an alerting sink behind an interface
  (no vendor coupling). **Each finding records both versions**: the capture's
  `applied_ruleset_version` (what the client actually redacted with, from Task 3)
  and the audit's own evaluation version. Without the pair, a finding raised
  after a ruleset update cannot be classified — a genuine client-side hole and a
  rule that simply did not exist yet look identical, and the second kind trains
  people to ignore the alert.
- Re-running the audit for an older capture uses the same pairing, so a finding
  is reproducible: given the two versions, the evaluation can be repeated exactly.

**Tests:** the shared-corpus conformance suite (Go side must agree with the TS
side on every fixture); a synthetic leak triggers exactly one alert carrying both
version fields; a capture redacted under an older ruleset produces a finding
classified against that version, not the current one; the service provably
performs no write to capture data.

---

## Task 13: Retention windows, hard delete, and the audit log

**Deliverables**

- Per-workspace retention windows, configurable, resolved from the Task 10 policy
  object. The **default value is a compliance judgment owned by
  Security/Compliance** (spec §22 open question 4) — implement it as a required
  policy value with no code-level default, so a workspace cannot be created
  without one being chosen.
- A hard-delete job that purges blobs and metadata together. **Not as one
  transaction** — object storage cannot enlist in a Postgres transaction, so
  there is no such unit to write. Either ordering fails on its own: deleting rows
  first orphans blobs when the job dies; deleting blobs inside the transaction
  lets a rollback restore metadata that now points at objects already gone.
  Use a **durable purge state machine** instead, driven by a `purge_jobs` row per
  capture that advances through explicit states and is safe to resume at any one:
  1. `pending` — the capture has passed its retention window.
  2. `tombstoned` — the capture is marked deleted in one Postgres transaction and
     stops being readable, exportable, and share-resolvable from this moment.
     Everything user-visible is finished here; the rest is reclamation.
  3. `blobs-deleting` — object deletes issued **idempotently** (a delete of an
     already-absent key is a success, not an error), the object keys held in the
     job row so a resume knows exactly what remains.
  4. `blobs-deleted` → `purged` — rows removed, job row retained briefly as
     evidence of completion.
  A reconciliation sweep re-drives jobs stuck in any intermediate state and
  reports objects whose key is in no job row and no live asset — the orphan
  detector, which is what makes "no orphans" a claim anyone can verify rather
  than a hope.
  Tombstone-before-reclaim is what buys correctness: the *compliance* deadline is
  met at step 2, so a slow or retrying blob delete is an operational matter, not
  a retention breach.
- On expiry the capture transitions to `expired` (the Phase 0 state) before purge,
  and `expired` is not viewable.
- **Append-only audit log** covering: capture creation, access, export,
  share-link resolution, MCP read (schema present now, written in Phase 2), and
  deletion. Immutability enforced by the database triggers from Task 3.
- Audit entries are written **in the same transaction** as the action they
  record, so an action can never succeed unaudited.

**Tests:** a kill at **each** purge state resumes to `purged` with no orphans
(drive the job to every intermediate state and restart from it); a repeated blob
delete succeeds rather than erroring; a tombstoned capture is immediately
unreadable and unshareable even while its blobs still exist; the reconciliation
sweep detects a deliberately orphaned object; an expired
capture is not viewable or share-resolvable; every audited action writes exactly
one entry; the audit log rejects update and delete.

---

## Task 14: Slack integration

Extends `packages/trackers` with the third provider. The interface was validated
by GitHub and GitLab in Phase 0; this task must not need to change it. **If it
does, that is a finding worth reporting, not a licence to reshape the interface
quietly.**

**Deliverables**

- `packages/trackers/src/slack.ts` implementing the existing `TrackerProvider`
  shape, or — if posting a message genuinely does not fit "create an issue" — a
  sibling `NotifierProvider` interface with an explicit note in the report about
  why the existing one did not fit.
- OAuth without a shipped client secret, consistent with the Phase 0 rule.
- Posts the replication document rendered for Slack (blocks, not a wall of
  markdown), a link back to the capture, and the video as an attachment or a
  signed link depending on size.
- Calls the `capture-core` ready gate before building any payload.
- Per-project target binding through `integration_bindings`.

**Tests:** contract tests against recorded Slack API fixtures; a non-`ready`
capture is refused; binding resolution per project.

---

## Task 15: JS SDK with metadata()

**Deliverables** in `packages/sdk/`:

- A small browser SDK a host application embeds to enrich captures:
  `htr.metadata({ userId, tenant, buildSha, featureFlags })` — arbitrary
  JSON-serializable context injected into `Capture.metadata`.
- **SDK-injected metadata is redaction-scanned** exactly like any other payload
  (spec §5.1 says so explicitly). A host application passing a patient name into
  `metadata()` must not create a leak. Route it through the same redactor; a
  dropped metadata field increments nothing user-facing but is recorded in
  `rulesApplied`.
- `source: 'sdk'` on captures originating this way.
- The SDK must not bundle the whole of `capture-core`; expose a minimal surface
  and keep the payload small. State the measured bundle size in the report.
- Zero dependencies. It runs inside other people's applications.

**Tests:** metadata redaction proven with corpus fixtures; the SDK working with
no extension installed; bundle-size assertion in CI.

---

## Out of scope for this plan

- `router`, `summarizer`, `mcp-gateway`, webhooks, transactional-outbox dispatch
- Server-backed Recording Links (Phase 2, gated on `TRD-SEC-003`)
- MCP server, CLI
- iOS ReplayKit, recurring-bug pattern detection, helpdesk plugin
- Any CRDT. The decision is last-write-wins ([ADR-012](../../decisions/ADR-012-sync-protocol.md))
