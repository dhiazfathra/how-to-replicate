# Phase 2 Implementation Plan — agents and external capture

**Derives from:** [design spec](../specs/2026-08-04-how-to-replicate-design.md) §13, §14, §19 "Phase 2 — agents and external capture"
**Depends on:** [Phase 1 plan](2026-08-04-phase-1-implementation.md) complete and merged
**Date:** 2026-08-04

Phase 2 opens two doors that were deliberately shut until now: **external
reporters uploading to us**, and **agents reading capture context**. Both are
trust-boundary changes, and both are gated accordingly.

Phase 3 work (iOS, pattern detection, helpdesk plugin) is out of scope.

---

## Global Constraints

### Invariants — Phase 0's five and Phase 1's four, all still binding

1. No unredacted capture is viewable, exportable, or routable. Gate is
   `state === 'ready'`.
2. No unblurred frame exists in a persisted artifact.
3. LLM steps cite event IDs that exist, or they are dropped.
4. Lost fidelity is visible.
5. `packages/capture-core` depends on nothing in `clients/`.
6. The server never becomes the read path for local-first clients.
7. `redaction-audit` is an alarm, not a filter — **for authenticated captures.**
   See invariant 11, which is the sole exception and applies only to the
   anonymous path.
8. Eviction requires `sync.manifestComplete === true`.
9. Nothing unredacted is transmitted from a client we control.

### Invariants added by Phase 2

- **10. `TRD-SEC-003` is a hard prerequisite for the server-backed Recording
  Link.** Not a checkbox at the end — the anonymous upload path does not ship, is
  not enabled behind a flag, and is not exposed on a staging host until it is
  signed off. Following the `TRD-SEC-002` PII-handling precedent (spec §17).
- **11. On the anonymous upload path, redaction happens server-side in a
  non-durable inspection buffer before any durable write.** An anonymous
  external reporter's machine is not a trust boundary we control. Only the
  redacted result reaches durable storage. Nothing unredacted is ever written
  to the object store or to `sync-gateway`'s retained storage — not
  temporarily, not "just the metadata", not to a log line. On this path
  `redaction-audit` is a **blocking gate**: an upload that fails inspection is
  **rejected outright**, never partially stored and merely flagged.
- **12. The MCP projection is read-only.** An agent investigating a bug has no
  reason to mutate the evidence. There is no write verb, no tool that
  modifies state, and no server-side path from an MCP request to a mutation.
- **13. Every MCP read writes an `audit_log` entry with the resolved agent
  identity.** An unattributable read is a rejected read.
- **14. One retry-and-backoff implementation.** Tracker dispatch and webhooks
  both go through the same transactional outbox. Building a second delivery
  mechanism is a defect, not an optimization.

### Conventions

- Local-first conventions from Phase 0 and 1 continue to apply to every client.
- Client-minted ULIDs; the server assigns revisions only.
- Events and assets remain immutable.
- **The CLI is an ingest client, not a privileged one.** It uses the same ingest
  path as the extension, with `source: 'cli'`. It gets no bypass of the ready
  gate, the redactor, or RBAC.

### Engineering standards

- **100% coverage of branches and edge cases**, Go and TypeScript alike.
- `pnpm lint` and `golangci-lint run` both exit 0 before a task is DONE.
- Conventional commits. Update the README for anything affecting setup or usage.
- No new runtime dependency beyond the ones this plan names.

### Stack decisions (already made — do not re-decide)

| Concern | Decision |
|---|---|
| Services | Go, as Phase 1: chi + otelhttp, ConnectRPC for browser, gRPC internally |
| Outbox | Transactional outbox table (created in Phase 1 Task 3) + a poller in `router` |
| MCP | The official MCP Go SDK; stdio and HTTP transports |
| CLI | Go, `cobra`, distributed as a single static binary |
| Inspection buffer | In-memory, size-bounded, never written to disk |

### Repository layout added by this phase

```text
services/
  router/             GitHub/GitLab/Slack/webhook dispatch + outbox poller
  summarizer/         server-side doc generation for shared/anonymous captures
  mcp-gateway/        read-only MCP projection
cli/                  agent-facing CLI
```

---

## Task 1: router — transactional outbox and dispatch

Spec §14 "Webhooks" and §11 `router`. One delivery mechanism for everything
(invariant 14).

**Deliverables** in `services/router/`:

- **Outbox poller** over the `outbox` table created in Phase 1 Task 3. Rows are
  written **in the same transaction as the event that caused them**, which is
  what makes the outbox transactional and what makes "capture became ready" and
  "a dispatch is pending" atomic.
- Delivery semantics: at-least-once with exponential backoff and jitter, a
  bounded attempt count, and a dead-letter state that is visible rather than
  silent. Duplicate delivery is possible by design — every dispatch carries an
  **idempotency key**, which is the outbox row's ULID and is **stable across all
  retries of that row**. It is the receiver's only defence against duplicate
  issues, so it is part of the contract, not a log field.
- Dispatch targets behind one interface: GitHub, GitLab, Slack (all three via the
  existing `packages/trackers` contracts through a thin Go-side adapter or a
  direct Go implementation — choose one, justify it in the report, do not build
  both), and generic webhooks.
- Per-project routing resolved from `integration_bindings`.
- **The ready gate applies server-side too.** A capture that is not `ready` never
  produces an outbox row and never dispatches. Assert it at the row-writing site,
  not only at the dispatch site.
- **Ordering, stated precisely** — "in order for the same capture" does not
  survive retries and dead letters on its own. A row that is backing off would
  otherwise let its successor overtake it, and concurrent pollers would race for
  the same capture. So:
  - Each outbox row carries a monotonic **per-capture sequence number**, assigned
    in the same transaction that writes the row.
  - A poller takes a **lease on the capture**, not on the row — one poller owns a
    capture's queue at a time, so two pollers can never dispatch two rows of the
    same capture concurrently. Leases expire so a dead poller does not block a
    capture forever.
  - Within a capture, the poller dispatches strictly in sequence order and does
    not advance past a row that is retrying. Head-of-line blocking is the
    intended behaviour: an out-of-order issue comment is worse than a late one.
  - **A dead-lettered row does not block its successors forever.** It is a
    documented gap: the poller records the skipped sequence number on the
    capture, advances, and the gap is visible in the dead-letter view. Blocking
    a capture's dispatches permanently on one poisoned row trades a visible gap
    for a silent stall.
  - Dispatches across different captures interleave freely.

**Tests:** testcontainer Postgres. A crash between the causing transaction and
the dispatch still delivers on restart; a permanently failing target
dead-letters rather than retrying forever; a non-`ready` capture never enqueues;
per-capture ordering under concurrent pollers (run two); **a retrying row is not
overtaken by its successor**; **a dead-lettered row records a visible gap and its
successors still dispatch**; an expired lease is reclaimed by a second poller
without duplicate concurrent dispatch; retries of one row all carry the same
idempotency key.

---

## Task 2: Webhooks on capture-ready

Spec §14. Rides the Task 1 outbox — no second delivery path.

**Deliverables**

- Webhook subscription management in `capture-api`: per-workspace endpoints,
  per-project filters, a shared secret minted server-side.
- Fire on **capture-ready** only. The payload carries the replication document,
  the capture deep link, `fidelity`, `withheldEventCount`, and signed asset URLs
  — never raw assets, never anything that bypassed the redactor.
- **The Task 1 idempotency key travels in the signed material**, as a header
  covered by the signature (or a body field — pick one and document it). Task 1
  guarantees duplicate delivery is possible; a receiver that cannot recognize a
  duplicate creates a duplicate issue every retry, which is exactly the failure
  the outbox was supposed to make survivable. Retries of the same dispatch reuse
  the same key.
- **Signed payloads:** HMAC-SHA256 over the raw body with a timestamped header,
  so a receiver can verify authenticity and reject replays. Document the
  verification recipe in the README.
- Enables the auto-create-issue plus auto-assign-by-brand/service-ownership flow
  named in the spec: a webhook receiver can act, and the assignment rules live
  in the receiver, not in us.
- **SSRF guard at the socket, not at the string.** Validating the URL at
  registration and again at dispatch still loses to DNS rebinding — the name
  resolves publicly when checked and privately a moment later, when the
  connection is actually made. Checking twice narrows the window; it does not
  close it. So:
  - Use an **SSRF-safe dialer**: a custom `DialContext` that inspects the
    **resolved IP at connect time** and refuses private, loopback, link-local,
    multicast, unique-local, and cloud-metadata ranges (v4 and v6, including
    v4-mapped-v6 forms). This is the check that cannot be raced, because it runs
    after resolution and before the packet.
  - Allow **only `http` and `https`** schemes.
  - **Do not follow redirects.** A public endpoint that 302s to `169.254.169.254`
    defeats every pre-flight check. If redirects are ever needed, validate each
    hop through the same dialer — but the default is refusal.
  - Keep the registration-time check as a usability affordance (fail fast on an
    obviously bad endpoint), not as the security control.

**Tests:** signature verification vectors; replay rejection outside the timestamp
window; the dialer refuses every private range and the metadata address, in v4,
v6, and v4-mapped-v6 form; **a DNS name that resolves public-then-private is
refused at connect** (rebinding, with a stub resolver); **a redirect to a private
address is not followed**; a non-http(s) scheme is refused; a
`fidelity: 'degraded'` capture carries the flag through to the payload; the
idempotency key is present, signed, and stable across retries.

---

## Task 3: summarizer — server-side document generation

Spec §11. Needed because a shared or anonymous capture has no client of ours to
generate its document.

**Deliverables** in `services/summarizer/`:

- Server-side generation of the replication document for shared and anonymous
  captures, over the same event timeline.
- **The deterministic pass is the floor here too.** Port the Phase 0 step
  generator's semantics and prove agreement with a conformance suite driven by
  the **same golden timeline fixtures** as Phase 0 Task 7. A server document that
  differs from the client document for the same timeline is a defect.
- **Hallucination control is identical and non-negotiable** (invariant 3): every
  LLM-authored step cites `eventIds` that exist *and* is validated as describing
  what those events contain; failing steps are dropped individually; zero
  survivors discards the whole LLM pass; the deterministic document stands alone.
- LLM access via the internal gateway. **A workspace policy-configured
  `localOnly` never reaches this service** — those workspaces do not use
  server-side generation at all, and the service must reject such a request
  explicitly rather than quietly using the gateway.
- Generation failure is non-fatal: the capture keeps its deterministic document
  and stays `ready`.
- **Ordering against the ready transition**, since Task 2's webhook payload must
  carry a document and this task's LLM pass is allowed to fail or be slow:
  1. Generate the **deterministic** document and **persist it**.
  2. Transition to `ready` and write the outbox row — in one transaction, so a
     `capture-ready` dispatch can never reference a capture without a document.
  3. Run LLM enrichment afterwards, off the critical path. If it succeeds, the
     enriched document replaces the stored one and a **separate
     `capture-document-updated` dispatch** goes out through the same outbox.
  Receivers therefore always get a usable document immediately, and a better one
  later if there is one. Enrichment never delays or blocks `ready` — that is what
  "AI is convenience; redaction is compliance" means operationally.

**Tests:** the client/server conformance suite over shared fixtures; fabricated
`eventIds` dropped; zero-survivor discard; a `localOnly` workspace request
rejected, with an assertion that no gateway call was made; **the outbox row and
the `ready` transition commit together and never before the deterministic
document is persisted**; a slow enrichment does not delay `ready`; a failed
enrichment emits no update dispatch and leaves the deterministic document
intact; a successful late enrichment emits exactly one update dispatch.

---

## Task 4: Server-backed Recording Links — the anonymous ingest path

Spec §13 Phase 2 and [ADR-013](../../decisions/ADR-013-recording-links-two-stage.md).
**This is the highest-risk task in the entire program.** Read invariants 10 and
11 before writing anything.

**Prerequisite: `TRD-SEC-003` sign-off.** Do not begin implementation without it.
If it is not signed off, report `BLOCKED` immediately — that is the correct
outcome, not a reason to build it behind a flag.

**Deliverables**

- Upgrade `clients/recording-link` from Phase 0's export-only handoff: the same
  page, now with **token-scoped anonymous upload** straight into `sync-gateway`,
  no download step. The Phase 0 export path remains available.
- Link tokens: workspace- and project-scoped, expiring, revocable, single-purpose
  (upload only — they grant no read).
- **The inspection buffer** in `sync-gateway`:
  - An uploaded capture lands in a **non-durable, size-bounded, in-memory**
    buffer. It is never written to disk, never to the object store, never to a
    temp file, never to a log, and never to `sync-gateway`'s retained storage.
  - `redaction-audit` runs as a **blocking gate** over the buffer contents.
  - **Pass** → only the redacted result is written to durable storage.
  - **Fail** → the upload is **rejected outright**. Not partially stored. Not
    stored-and-flagged. The buffer is zeroed and the reporter gets an actionable
    rejection.
  - The buffer has a hard size cap and a hard time cap; exceeding either is a
    rejection, not a spill to disk.
- Client-side redaction still runs on the reporter's machine first — it is the
  first pass, not the only one. Server-side inspection exists because that machine
  is not a trust boundary we control, and the two are not redundant.
- Video: the reporter's browser still blurs **pre-encode** (invariant 2). The
  server cannot un-blur and must not transcode on this path; inspection covers
  the event stream, metadata, and screenshots. State plainly in the report what
  the server inspection can and cannot see inside an encoded video, and record
  the residual risk.
- Rate limiting and abuse controls on an unauthenticated endpoint: per-token,
  per-IP, and global caps, with a circuit breaker.

**Tests:** a fixture containing PHI that the client redactor misses is
**rejected**, and the test asserts no durable write occurred anywhere (object
store empty, no rows, no temp files, log scan clean); buffer size and time caps
reject rather than spill; a revoked or expired token is refused; an upload token
grants no read anywhere in the API; rate limits trip.

**Reviewer note for whoever reviews this task:** the "no durable write on
rejection" assertion is the review's centre of gravity. A green test that only
checks the API response is not sufficient evidence.

---

## Task 5: mcp-gateway — read-only capture projection

Spec §14 and [ADR-014](../../decisions/ADR-014-mcp-read-only-projection.md).
Read invariants 12 and 13 first.

**Deliverables** in `services/mcp-gateway/`:

- An MCP server exposing captures as resources so an agent can pull full bug
  context into a session:
  - the replication document,
  - the filtered event timeline,
  - network requests,
  - the environment snapshot,
  - asset URLs (signed, expiring — never raw blobs inline).
- **Read-only. No write verb exists.** No tool mutates state; no code path leads
  from an MCP request to a mutation, a comment, a state transition, or a
  dispatch. Prove it with a test that enumerates every registered tool and
  resource and asserts none is a mutation.
- **Every read writes an `audit_log` entry with the resolved agent identity.** An
  identity that cannot be resolved is a rejected read, not an anonymous one.
  **The audit write is fail-closed and happens first:** commit the entry, then
  return content. If the audit store is unavailable or the write cannot commit,
  **return no content** — invariant 13 says every read is audited, and a read
  served while the audit failed is an unaudited read no matter how it is logged
  afterwards. Availability of the projection is worth less than its
  accountability.
  Also define the surrounding cases explicitly:
  - **Duplicate/retried requests** carry a client request ID; a retry of an
    already-audited request is idempotent and does not write a second entry.
  - **Denied attempts are audited too** — a rejected read is exactly the event a
    security reviewer most wants to see, and recording only successes makes the
    log useless for that purpose.
- Workspace-scoped RBAC identical to `capture-api` — an agent sees exactly what
  the identity it acts for could see, never more.
- **Only `state === 'ready'` captures are projected** (invariant 1).
- Resource content is the already-redacted stored content. The gateway performs
  no redaction of its own and must not be described as a redaction boundary.
- Transports: stdio for local agent use, HTTP for hosted use. **Both must prove
  identity; neither may take it from MCP input.** An agent or workspace ID
  supplied in a request is a claim, not a credential — accepting one would let
  any local process read any workspace by asserting it.
  - **HTTP:** the same bearer token verification as `capture-api`, through
    `internal/auth`.
  - **stdio:** the session is bound to the **CLI credential** from Task 6 — the
    gateway reads the same OS-keychain-or-0600-file token the CLI writes, and
    resolves identity from it. A stdio session that cannot be bound to a
    credential is **rejected**, not treated as a trusted local caller. "It is
    running on my machine" is not an identity; on a shared or compromised host
    it is not even a boundary.

**Tests:** the enumerate-all-tools no-mutation assertion; an unresolvable
identity is rejected; every successful read produces exactly one audit entry
naming the agent; **a read is refused when the audit write fails, and returns no
content**; a retried request with the same request ID writes one entry, not two;
a denied read is audited; **a stdio session with no bound credential is
rejected**, and an agent/workspace ID passed in the request body is ignored in
favour of the credential; a non-`ready` capture is invisible; cross-workspace
projection returns nothing and does not disclose existence.

---

## Task 6: CLI — agent-submitted captures

Spec §14. The direction here is inbound: an agent records a walkthrough of its
own PR and submits it, giving humans a reviewable trail of agent work.

**Deliverables** in `cli/`:

- Go, `cobra`, single static binary. Commands, minimally:
  - `htr capture submit` — ingest a capture from a local recording plus a
    timeline file, through the **same ingest path as the extension**, with
    `source: 'cli'`. No privileged bypass of the ready gate, the redactor, or
    RBAC (Phase 2 conventions).
  - `htr capture list` / `htr capture get` — read-only, RBAC-scoped.
  - `htr llm` — drives the local-LLM native messaging host, so the CLI and the
    extension **share one model configuration** rather than each having their
    own. Read the same config the extension's native messaging host uses.
- Redaction runs before submission, using the same ruleset bundle format. A CLI
  submission that skips redaction must be impossible, not merely discouraged —
  there is no `--no-redact` flag and no code path without the redactor.
- Auth: device-flow style login writing a short-lived token to the OS keychain
  where available, a mode-0600 file otherwise. Never a long-lived PAT in an
  environment variable as the documented happy path.
- `--json` is available on every command, because the primary caller is an agent.

**Tests:** submission goes through the redactor (assert with corpus fixtures);
no flag combination bypasses it; RBAC enforced on read commands; the shared LLM
config is read from the same location the extension uses; token file
permissions.

---

## Task 7: Native-messaging host distribution

Spec §22 open question 1. It blocks the local-LLM path leaving internal beta, and
Task 6 depends on the same host.

**Deliverables**

- A host manifest and binary installer for the `claude` CLI bridge, covering
  macOS and Linux dev machines.
- **Decision required from Platform/IT** (the spec names them as owner): bundle
  with existing dev-machine provisioning, or ship a separate installer. Ask
  before building; do not pick unilaterally. If no answer is available, report
  `BLOCKED` with the question — this is a distribution decision with support
  consequences, not an implementation detail.
- Whichever is chosen: idempotent install, a verifiable version, an uninstall
  path, and a `htr doctor` command reporting whether the host is installed,
  which version, and whether the extension can reach it.
- **Be honest about what the extension-ID check actually proves.** Chrome passes
  the calling extension's origin as the host's first argument, and the manifest's
  `allowed_origins` is enforced *by the browser* when the browser launches the
  host. A local process can execute the same binary directly and pass an
  allow-listed origin string itself — so binary-side ID validation is a check on
  an **unauthenticated argument**, not proof of browser provenance. It is worth
  keeping (it stops accidents and casual misuse) but it is not the boundary.
  Two acceptable resolutions; pick one and say which in the report:
  - **Narrow the guarantee.** State plainly that the host trusts any local
    process running as the user, and that the control is the OS user boundary.
    Then ensure the host holds no secret worth stealing — it drives a local
    model and nothing else.
  - **Add a real channel.** Bind the host to an OS-protected authenticated
    channel (a per-user socket with peer credential checks, or a token the
    extension obtains through a browser-mediated step and presents per session).
  What is not acceptable is documenting the ID check as though it blocked
  arbitrary local processes, because that is a guarantee the mechanism cannot
  make.

**Tests:** install/uninstall idempotency; version reporting; the extension-ID
allow-list rejecting an unknown caller; **direct invocation of the binary with a
forged allow-listed origin**, asserting whichever behaviour the chosen resolution
specifies (rejected under a real channel; accepted-and-documented under the
narrowed guarantee — never silently accepted while the docs claim otherwise);
`htr doctor` output for each failure mode.

---

## Out of scope for this plan

- iOS ReplayKit capture and the Flutter SDK shim (Phase 3)
- Recurring-bug pattern detection (Phase 3)
- Helpdesk plugin (Phase 3)
- Any write verb on the MCP surface, in any form, ever
- Server-side transcoding on the ingest or redaction path
