# How to Replicate — Design Spec

**Status:** Approved for planning
**Date:** 2026-08-04
**Owner:** Aldhiaz Fathra Daiva (Eng Lead)
**Derives from:** PRD-JAMCLONE-001 (Bug & Feedback Capture Platform)
**Reference product:** [jam.dev](https://jam.dev/)

This spec covers all four delivery phases. It supersedes PRD-JAMCLONE-001 wherever
the two disagree; every such disagreement is called out explicitly under
[§20 Deviations from the PRD](#20-deviations-from-the-prd).

---

## 1. Product thesis

The name is the specification. Every capture produces one artifact: a **How to
Replicate document** — an ordered list of repro steps, each step linked to the
console, network, and interaction events that prove it happened, and to the exact
video timestamp where it is visible.

The screen recording is the *input*. The document is the *product*. Screen
recording is commoditized (Loom, CleanShot); the defensible work is converting a
recording plus ambient browser state into a document an engineer — or an agent —
can act on without asking the reporter a single follow-up question.

This framing is the scope guard. Any proposed feature that does not improve the
fidelity of the replication document, or the speed with which it reaches a human
or an agent, is out of scope.

## 2. Goals

1. One-click capture with zero manual write-up of technical context.
2. Console logs, network requests, device metadata, and interaction trail attached
   automatically to every capture.
3. A replication document generated without the reporter typing prose.
4. One-click routing into GitHub Issues, GitLab Issues, and Slack.
5. Machine-readable capture context for coding agents via MCP and CLI.
6. No PHI or PII leaves the reporter's machine unredacted — ever, on any path.

## 3. Non-goals

- Session-replay analytics (heatmaps, funnels, cohort analysis). Not this product.
- Our own issue tracker. We integrate; we do not replace.
- Billing, plans, or usage metering. Internal tool.
- Server-side video transcoding **on the ingest or redaction path**. Blur is
  composited pre-encode on the client and stays that way in every phase. A Phase 1
  transcode purely for Safari playback compatibility — operating on an
  already-blurred source — is a separate question left open in
  [ADR-006](../../decisions/ADR-006-client-side-capture-via-cdp.md).
- Native Android capture. Not planned.

## 4. Architecture

### 4.1 Two decisions shape everything else

**Phase 0 ships zero new server-side code.** Not "no network" — the client may call
APIs that already exist (GitHub, GitLab, the internal LLM gateway, a localhost
model server). It means we operate no service of our own until Phase 1. See
[ADR-002](../../decisions/ADR-002-client-only-phase-0.md).

**The client is the source of truth, permanently.** IndexedDB is not Phase 0
scaffolding to be replaced by a database later. When the server arrives in Phase 1
it is a *sync target* — it never becomes the thing the UI reads from. See
[ADR-001](../../decisions/ADR-001-local-first-architecture.md).

Everything downstream follows from those two. Capture IDs are minted in the
browser ([ADR-003](../../decisions/ADR-003-client-minted-ulids.md)), so a capture is
complete and valid before any server has heard of it. The UI never awaits a round
trip. Redaction runs client-side because there is no server pass to fall back on.

### 4.2 Component map

```text
┌─ clients/extension ────────────────────────────────────────┐
│  MV3 service worker    capture orchestration, lifecycle     │
│  offscreen document    MediaRecorder + blur compositing     │
│  content script        DOM observation, interaction trail   │
│  chrome.debugger (CDP) Network / Console / Runtime domains  │
└────────────────────────────────────────────────────────────┘
        │ uses
        ▼
┌─ packages/capture-core ────────────────────────────────────┐
│  event model · redaction engine · step generator ·          │
│  storage layer · observable capture store                   │
│  framework-free, no browser-extension APIs                  │
└────────────────────────────────────────────────────────────┘
        ▲                    ▲                    ▲
        │                    │                    │
 clients/viewer      clients/recording-link       cli/
 (⌘K, playback)      (getDisplayMedia, P0)        (P2)

┌─ packages/llm ──────────┐  ┌─ packages/trackers ──────────┐
│ HTTP provider           │  │ GitHub (device flow)          │
│  → internal gateway     │  │ GitLab (OAuth PKCE)           │
│  → localhost OpenAI-API │  │ Slack (P1)                    │
│ NativeMessaging provider│  │                               │
│  → claude CLI           │  │                               │
└─────────────────────────┘  └───────────────────────────────┘

── Phase 1+ ────────────────────────────────────────────────
services/sync-gateway     mutation intake, delta fan-out
services/capture-api      read, share links, comments, RBAC
services/redaction-audit  verification pass (audit, not primary)
services/summarizer       server-side doc generation for shared captures
services/router           GitHub/GitLab/Slack/webhook dispatch + outbox
services/mcp-gateway      read-only MCP projection
```

`packages/capture-core` is the deep module. It knows nothing about Chrome
extensions, React, or HTTP — it takes raw event streams in, applies redaction, and
emits a replication document plus a persisted capture. Three separate clients
consume it. Its interface is the boundary that makes the extension, the Recording
Link page, and the CLI independently testable.

## 5. Data model

### 5.1 Capture

```ts
type CaptureState =
  | 'recording'    // active; buffer filling
  | 'redacting'    // stream ended; rules applying
  | 'composing'    // redaction done; document being generated
  | 'ready'        // viewable, exportable, routable
  | 'failed'       // redaction or encode failed; NOT viewable
  | 'expired';     // retention window elapsed; assets purged

type Capture = {
  id: string;                  // ULID, minted client-side
  workspaceId: string | null;  // null while purely local (pre-Phase 1)
  projectId: string | null;    // brand surface
  source: 'extension' | 'recording-link' | 'sdk' | 'cli' | 'ios';
  state: CaptureState;
  fidelity: 'full' | 'degraded';   // degraded when CDP detached — see §7.4
  createdAt: string;               // ISO 8601, client clock
  epoch: number;                   // performance.timeOrigin-anchored start
  env: EnvSnapshot;
  metadata: Record<string, JsonValue>;  // SDK-injected, redaction-scanned
  doc: ReplicationDoc | null;
  assets: AssetRef[];
  withheldEventCount: number;      // events dropped by fail-closed redaction
  sync: {                          // Phase 1+; inert before that
    revision: number;
    lastPushedAt: string | null;   // metadata push only — see manifestComplete
    manifestComplete: boolean;     // true only once the server has verified every
                                   // asset's size and hash against the manifest
                                   // (ADR-012 §Assets) — this, not lastPushedAt,
                                   // is what "synced" means for eviction purposes
    dirtyFields: string[];
  };
};
```

`epoch` is anchored to `performance.timeOrigin`, and every event timestamp is a
**millisecond offset from it** rather than a wall-clock time. Wall clocks skew,
drift, and jump across DST and NTP corrections; offsets do not. This makes
video-to-event alignment exact and makes a capture recorded on a laptop with a
badly wrong clock still perfectly internally consistent.

### 5.2 Event timeline

One append-only log holds every capture-time event in a single ordered stream.
Four kinds come from ambient browser observation (`console`, `network`,
`interaction`, `navigation`); two are generated by the capture pipeline itself —
`lifecycle` (state transitions, fidelity changes, e.g. the CDP-detach handoff in
[ADR-006](../../decisions/ADR-006-client-side-capture-via-cdp.md)) and `annotation`
(a reporter's own inline note, timestamped into the timeline like any other event):

```ts
type CaptureEvent = {
  id: string;         // ULID — monotonic, so sort order needs no tiebreaker
  captureId: string;
  t: number;          // ms offset from capture.epoch
  kind: 'console' | 'network' | 'interaction' | 'navigation'
      | 'lifecycle' | 'annotation';
  payload: ConsolePayload | NetworkPayload | InteractionPayload
         | NavigationPayload | LifecyclePayload | AnnotationPayload;
  redaction: {
    rulesApplied: string[];        // ruleset rule IDs
    fidelity: 'full' | 'redacted' | 'dropped';
  };
};
```

Six kinds, one table. Playback scrubbing, step generation, and MCP export all read
one ordered source instead of joining several and reconciling several notions of
time. See [ADR-004](../../decisions/ADR-004-append-only-event-timeline.md).

**Events are immutable.** Once written, an event is never updated or deleted
(retention purges the whole capture, not individual events). This is what makes
[§10 sync](#10-sync-engine-phase-1) nearly trivial.

### 5.3 The replication document

```ts
type ReplicationDoc = {
  title: string;
  summary: string;
  steps: Step[];
  expected: string | null;
  actual: string | null;
  generator: 'deterministic' | 'llm';
  generatorModel: string | null;   // e.g. 'claude-opus-5' — provenance matters
};

type Step = {
  n: number;
  text: string;
  eventIds: string[];   // every step cites the events that evidence it
  tVideo: number | null; // ms into the recording
};
```

`eventIds` is the load-bearing field. It makes each step clickable (scrub the
video, highlight the network row), and it is how LLM output gets validated —
see [§8.3](#83-hallucination-control).

### 5.4 Supporting records

| Record | Purpose | Mutability |
|---|---|---|
| `asset` | video / screenshot / HAR blob, chunked | immutable |
| `redaction_ruleset` | versioned rule bundle | replaced wholesale, never edited |
| `mutation` | queued local change awaiting sync (Phase 1) | append-only |
| `comment` | discussion on a capture (Phase 1) | append-only |
| `share_link` | signed, expiring, revocable token (Phase 1) | revocable |
| `workspace` / `project` | tenancy and brand-surface scoping (Phase 1) | mutable |
| `integration_binding` | tracker/Slack target per project (Phase 1) | mutable |
| `audit_log` | capture create / access / export / delete (Phase 1) | append-only |

## 6. Capture pipeline

```text
 trigger
    │
    ▼
 recording ──── ring buffer (Instant Replay) or explicit start
    │            console + network + interaction + video frames
    │            blur composited pre-encode (§7.2)
    ▼
 redacting ──── ruleset applied to every buffered event
    │            fail closed: unapplicable rule ⇒ event dropped
    │            failure ⇒ state = failed, capture NOT viewable
    ▼
 composing ──── deterministic step generation (always)
    │            LLM enrichment (when a provider is reachable)
    ▼
  ready ─────── viewable · exportable · routable
```

**The gate:** export, share, route, and MCP read all require `state === 'ready'`.
There is no code path that surfaces a capture in `recording`, `redacting`,
`composing`, or `failed`. See [ADR-005](../../decisions/ADR-005-fail-closed-redaction-gate.md).

### 6.1 Instant Replay

A rolling buffer holds roughly the last 120 seconds so a reporter never has to
predict a bug before it happens. Constraints:

- Events: ring buffer capped by count **and** bytes (default 20 000 events / 8 MB),
  evicting oldest first.
- Video: `MediaRecorder` in timeslice mode writing fixed-duration chunks into a
  bounded deque; chunks past the window are released.
- The buffer holds **already-redacted** events. Redaction is applied on ingest into
  the buffer, not on capture finalize — otherwise a reporter who never triggers a
  capture still has raw PHI sitting in extension memory.

That last point is a deliberate cost: redaction runs continuously while recording,
not once at the end. It buys the property that unredacted PHI has no resting place.

## 7. Redaction — the compliance core

Dermaesthetics is healthcare-adjacent. Patient data will appear in DOM and network
payloads. In Phase 0 there is no server-side pass, so **client-side redaction is
not the first line of defense — it is the only one.** Everything in this section is
a hard requirement, not a hardening measure.

### 7.1 Ruleset

Versioned, signed bundle delivered via extension enterprise policy, refreshed on a
timer. Rule classes:

| Class | Applies to | Example |
|---|---|---|
| field-path denylist | JSON bodies, by pointer | `/patient/nik`, `/*/dob` |
| header denylist | request/response headers | `authorization`, `cookie`, `x-patient-id` |
| pattern class | any string value | NIK (16-digit), BPJS, MRN, phone, email, DOB |
| DOM selector mask | text nodes in captured DOM | `[data-phi]`, `.patient-name` |
| video blur region | selector → screen rect | `[data-phi]`, `input[type=password]` |
| origin allow-list | whole capture | capture permitted only on listed origins |

Ownership: **Security/Compliance owns the ruleset contents; Engineering owns the
enforcement mechanism and its test corpus.** (Resolves PRD open question #4.)

### 7.2 Video blur happens before encoding

There is no server transcode step, so blur cannot be applied later. The display
stream is drawn to an `OffscreenCanvas`, blur rectangles — derived from selector
matches, with bounding boxes re-sampled each frame to track scroll and layout — are
composited on top, and **the canvas stream is what feeds `MediaRecorder`.**

Consequence: no unblurred frame ever exists in any persisted artifact. Only the
live, in-memory display stream is unblurred.

If the compositor misses its frame budget (slow machine, large blur set), the
capture **degrades to screenshot-only rather than emitting unblurred video.** Fail
closed, always.

### 7.3 Network and DOM

Redaction runs before the write to IndexedDB, never after. Bodies are truncated to
32 KB and field-redacted; non-allow-listed content types have bodies dropped and
keep only status, timing, and size. Off-allow-list origins produce no capture at
all — not even a screenshot, not even metadata.

### 7.4 Degraded fidelity is visible, not silent

Opening DevTools detaches `chrome.debugger`. When that happens the extension falls
back to `webRequest` plus console monkey-patching, and stamps the capture
`fidelity: 'degraded'`. The viewer renders that badge prominently.

Silently losing console logs while presenting a confident-looking replication
document is worse than not capturing at all — an engineer would trust a document
that is quietly incomplete.

Likewise, `withheldEventCount` renders as "N events withheld by redaction policy"
so a reader knows the timeline has gaps and why.

## 8. Replication document generation

### 8.1 Deterministic pass — always runs, fully offline

Interaction and navigation events collapse into prose with no model involved:

- Noise reduction: scroll bursts coalesce, `mousemove` is dropped, repeated
  identical clicks fold into "clicked N times".
- Target naming, in priority order: accessible name → associated label → trimmed
  text content → `data-testid` → CSS selector.
- Output shape: `Clicked "Save changes" on /patients/123/edit`.

This is the floor. Every capture has usable steps with no LLM, no network, and no
gateway dependency.

### 8.2 LLM enrichment — when a provider is reachable

An LLM pass improves the title, writes the summary, infers expected-vs-actual, and
merges mechanical steps into readable ones. Two transports behind one interface
(see [ADR-007](../../decisions/ADR-007-pluggable-llm-providers.md)):

| Provider | Transport | Use |
|---|---|---|
| `HttpProvider` | `fetch` to an OpenAI-compatible or gateway endpoint | internal LLM gateway (default); any localhost server — Ollama, LM Studio, litellm — by swapping base URL |
| `NativeMessagingProvider` | `chrome.runtime.connectNative` → host binary → `claude -p` | local Claude CLI, no HTTP listener |

The local paths are not a convenience feature. They are the configuration in which
capture text — which may contain PHI that survived redaction — never leaves the
machine at all. For the highest-sensitivity brand surfaces that is the only
acceptable configuration: those workspaces are policy-configured local-only, so
`HttpProvider` is allow-listed only for a localhost target, never the remote
gateway, and the fallback chain never crosses into a remote call on local-provider
failure (see ADR-007 §Decision).

Provider selection is policy-controlled per workspace, with an ordered fallback
chain that respects that boundary. Every provider failure is non-fatal: the
capture stays `ready` with deterministic steps. AI is convenience; redaction is
compliance. Only the latter blocks.

### 8.3 Hallucination control

An LLM writing repro steps will invent steps that did not happen. So:

**Deterministic steps are kept unconditionally — LLM enrichment only adds to that
floor.** Every LLM-authored step must both cite `eventIds` that exist in the
timeline **and** be validated as actually describing what those cited events
contain — existence of the ID is necessary but not sufficient. A step failing
either check is dropped, not flagged, individually, with no partial-failure
threshold: if zero LLM steps survive, the entire LLM pass is discarded and the
deterministic document stands alone.

An engineer must be able to trust that every step is backed by a real recorded
event that actually supports it. That guarantee is worth more than fluent prose.

## 9. Local storage budget

Local-first means video lives in IndexedDB. A two-minute 1080p WebM runs 20–40 MB.
This is the problem local-first creates, and it needs a real answer.

- Request `navigator.storage.persist()` before the first capture, so the browser
  does not evict the store under pressure.
- Check `navigator.storage.estimate()` before recording; refuse to start and prompt
  if the projected size exceeds remaining quota.
- Default cap: 2 GB or 40 captures per workspace, both policy-overridable.
- Video persists as chunked blob records, not one enormous value — bounded
  transaction size, resumable upload later.

Eviction, and this ordering is deliberate:

- **Phase 1+:** LRU over captures where `sync.manifestComplete === true` — the
  server has confirmed every asset, not merely that a metadata push happened. A
  capture whose video is still uploading is not eligible, no matter how stale
  `lastPushedAt` looks.
- **Phase 0:** nothing is synced anywhere, so **nothing is auto-evicted.** At the
  cap, new captures are blocked with an export-or-delete prompt.

Silently deleting the only copy of a bug report is unacceptable. Refusing to record
is merely annoying. See [ADR-009](../../decisions/ADR-009-local-storage-budget.md).

## 10. Sync engine (Phase 1)

The domain hands us a gift: **timelines and assets are immutable, so they can never
conflict.** The entire mutable surface is `title`, `summary`, `tags`,
`assignment`, plus append-only comments.

So: last-write-wins per field on server-received timestamp, and no CRDT. Reaching
for one here would be sophistication bought with no conflict to solve.

- **Transport:** ConnectRPC over HTTP for mutations and uploads; WebSocket for
  delta fan-out.
- **Push:** append-only mutation log over a closed field/operation set (`set`,
  `assign`, `append` — never a bare `field: string`), batched and flushed by the
  sync engine. Idempotent on mutation ULID, so a retry after an ambiguous failure
  is always safe. Conflicts resolve on `(server-received timestamp, revision,
  mutation.id)`, not a bare timestamp — see [ADR-012](../../decisions/ADR-012-sync-protocol.md).
- **Pull:** `since=<revision>` delta stream. The server owns revision numbering.
- **Assets:** presigned PUT straight to object storage against a server-selected,
  write-once object key; `sync.manifestComplete` flips to `true` only once the
  server has verified every uploaded asset's size and hash against the manifest.
- **Offline:** the mutation queue lives in IndexedDB and survives restart, capped
  at 5 000 mutations / 5 MB with a user-visible "pending sync" indicator rather
  than silent overflow. A week offline then a reconnect is a normal, tested path —
  not an edge case.
- **UI contract:** `capture.title = x; capture.save()` writes the local observable
  (synchronous re-render) and enqueues. Rollback happens only on explicit server
  reject.

See [ADR-012](../../decisions/ADR-012-sync-protocol.md).

## 11. Server services (Phase 1+)

Go, chi + otelhttp, ConnectRPC for browser clients, gRPC internally — consistent
with the ERP stack. Postgres mirrors the local schema. Blobs go to self-hosted
S3-compatible storage on Nutanix ([ADR-011](../../decisions/ADR-011-self-hosted-object-storage.md)) —
PHI-adjacent recordings stay inside the perimeter, playback via signed URLs, no
external CDN.

| Service | Responsibility |
|---|---|
| `sync-gateway` | mutation intake, revision assignment, delta fan-out |
| `capture-api` | reads, share links, comments, workspace RBAC |
| `redaction-audit` | re-runs the ruleset server-side and **alerts on any finding** |
| `summarizer` | server-side doc generation for shared/anonymous captures |
| `router` | GitHub / GitLab / Slack / webhook dispatch via transactional outbox |
| `mcp-gateway` | read-only MCP projection ([ADR-014](../../decisions/ADR-014-mcp-read-only-projection.md)) |

`redaction-audit` deserves a note: it is **not** a second chance to redact. By the
time data reaches it, unredacted content has already crossed the network boundary
and the incident has already occurred. It exists to detect ruleset gaps and page
Security — an alarm, not a filter.

Auth: existing IdP SSO, workspace-scoped RBAC. Render-first-authenticate-second —
if a local store exists, paint immediately and let a stale session fail on the next
sync delta.

## 12. Issue routing

GitHub Issues first, GitLab Issues second, behind one provider interface
([ADR-008](../../decisions/ADR-008-tracker-provider-abstraction.md)). Two real
implementations from day one, so the abstraction is validated rather than
speculative.

No client secret ships in the extension. The two providers differ in how they
manage that, and the difference is not optional:

| Provider | Flow | Why |
|---|---|---|
| GitHub | OAuth **device flow** (`/login/device/code`) | GitHub accepts PKCE parameters, but its code exchange still requires `client_secret` regardless — PKCE hardens, it doesn't remove the secret requirement. Device flow is the actual secretless path, and needs no redirect URI, which suits an extension. |
| GitLab | OAuth **PKCE** | GitLab's `code_verifier` fully substitutes for a client secret for public clients. |

Access tokens live in `chrome.storage.session` (cleared on browser exit); refresh
tokens are encrypted at rest in `chrome.storage.local`. Issues are attributed to
the real reporter, and access is centrally revocable — neither of which holds with
a pasted long-lived PAT.

Routing payload: the replication document as issue body, video and screenshots as
attachments, a deep link back to the capture, and structured labels derived from
`projectId` and detected error signatures.

## 13. Recording Links — two stages

Recording Links let an external clinic user report a bug with no login and no
install. They need somewhere to put the capture, which collides with Phase 0's
no-server rule. So the feature lands twice:

**Phase 0 — export-only handoff.** The Recording Link page captures locally via
`getDisplayMedia`, applies the same redaction ruleset through `capture-core`, and
produces a downloadable `.htr` bundle (video + timeline JSON + rendered markdown)
that the reporter attaches to an email or a support ticket. Honest about the
friction, but it validates the capture path against real external browsers and real
external machines before a server exists.

**Phase 2 — server-backed.** The same page, upgraded: token-scoped anonymous upload
straight into `sync-gateway`, no download step. For this path — unlike the
authenticated flow, where client-side redaction is complete before anything is
transmitted (§7) — redaction happens server-side in a **non-durable inspection
buffer** before any durable write, because an anonymous external reporter's
machine is not a trust boundary we control and cannot be relied on to have
redacted correctly. Only the redacted result reaches durable storage; nothing
unredacted is ever written to the object store or `sync-gateway`'s retained
storage. This makes `redaction-audit` a **blocking gate** for this specific
anonymous path, not the alarm-only role it plays for authenticated captures
(§17, §11) — an anonymous upload that fails inspection is rejected outright, never
partially stored and merely flagged.

See [ADR-013](../../decisions/ADR-013-recording-links-two-stage.md).

## 14. Agent surfaces (Phase 2)

**MCP server.** Exposes captures as MCP resources so Claude Code can pull full bug
context into an agent session: the replication document, filtered event timeline,
network requests, environment snapshot, asset URLs. **Read-only** — an agent
investigating a bug has no reason to mutate the evidence, and read-only makes the
audit story trivial. Every MCP read writes an `audit_log` entry with the resolved
agent identity.

**CLI.** Pushes artifacts *in*: an agent records a walkthrough of its own PR and
submits it as a capture, giving humans a reviewable trail of agent work. Same
ingest path as the extension, `source: 'cli'`. Also drives the local-LLM native
messaging host, so the CLI and the extension share one model configuration.

**Webhooks.** Fire on capture-ready. Enables auto-create-issue plus auto-assign by
brand/service ownership. Delivered through the same transactional outbox as tracker
dispatch — one retry-and-backoff implementation, not two.

## 15. iOS (Phase 3)

ReplayKit broadcast extension for screen capture; a Flutter-side SDK shim supplies
metadata and the interaction trail. No CDP equivalent exists on iOS, so network and
console capture require SDK-level instrumentation of the app's HTTP client —
captures are `fidelity: 'degraded'` by definition and labelled as such. Redaction
runs on-device with the same ruleset format.

## 16. Client performance

The viewer must feel instant, because it is opened dozens of times a day by
engineers triaging.

- **Read local, always.** The UI reads the in-memory observable store hydrated from
  IndexedDB. Navigation never fetches what is already local.
- **Granular observables.** Per-field observables so a delta re-renders one cell,
  not the list.
- **Build:** `target: 'esnext'`, no legacy polyfills, one vendor chunk per npm
  package so a dependency bump invalidates one chunk instead of the whole graph.
- **Preload:** critical chunks declared `rel=modulepreload` in `<head>` with
  matching `crossorigin`, collapsing the fetch→parse→fetch waterfall into one
  parallel batch.
- **Service worker** precaches route chunks after first load; combined with
  IndexedDB the viewer is fully offline-capable.
- **Inline critical CSS** plus a boot script restoring last-known shell tokens
  (sidebar width, dark mode) from `localStorage` before first paint.
- **Animate only `transform` and `opacity`.** Never `width`, `height`, `margin`,
  `top`, or `left` — those force layout on every subsequent element.
- **Durations:** 100 ms for quick transitions, 250 ms regular, 350 ms ceiling.
  Asymmetric — appear instantly, fade out over 150 ms.
- **`⌘K` palette** searching the local store: captures, projects, actions,
  navigation. No network request, so results are instant by construction.

## 17. Security and compliance

Non-negotiable, given ISO 27001 and PDP Law:

- Client-side redaction as primary control ([§7](#7-redaction--the-compliance-core)).
- Fail-closed capture state machine — no unredacted capture is ever viewable,
  exportable, or routable.
- Origin allow-list, pinned by enterprise policy, not user-editable.
- Encryption: TLS in transit; object-storage SSE and encrypted volumes at rest.
- Per-workspace retention windows with a hard-delete job that purges blobs and
  metadata together.
- Append-only audit log covering capture creation, access, export, share-link
  resolution, MCP read, and deletion.
- Secretless OAuth in the extension; short-lived access tokens in session storage.
- **`TRD-SEC-003` is a hard prerequisite** for the Phase 2 server-backed Recording
  Link, following the TRD-SEC-002 PII-handling precedent. Phase 0's export-only
  Recording Link does not gate on it, because nothing is transmitted to us.

## 18. Testing strategy

Target: 100% coverage of branches and edge cases, per project standard.

| Area | Approach |
|---|---|
| `capture-core` | Vitest unit tests; framework-free, so no DOM harness needed |
| Redaction | **Golden synthetic-PHI corpus** — Indonesian NIK, BPJS, MRN, DOB, phone, patient names across JSON bodies, headers, DOM text, and video frames. Asserts zero leakage. **Hard CI gate; a leak fails the build.** |
| Video blur | Fixture page renders known PHI text; capture, decode frames, OCR-assert no match. Slow — nightly job, with the non-video corpus gating every PR. |
| Step generator | Golden timeline fixtures → expected step output |
| LLM validation | Fuzz responses with fabricated `eventIds`; assert every fabricated step is dropped |
| Storage | Quota-exhaustion simulation; assert refuse-to-record rather than silent eviction |
| Sync (P1) | Property test — random mutation interleavings must converge to one state |
| Extension E2E | Playwright persistent context loading the unpacked extension |
| Trackers | Contract tests against recorded GitHub/GitLab API fixtures |
| Go services (P1) | Table-driven units; testcontainers integration (Postgres + MinIO) |

The redaction corpus is the single most important test asset in the repository.
Phase 0 has no server-side backstop, so that corpus is what stands between a
capture and a PHI leak. It gets reviewed by Security/Compliance, not just Eng.

## 19. Phased delivery

### Phase 0 — client only, zero new server-side code

- MV3 extension: screenshot, video, Instant Replay
- CDP-based console/network capture with `webRequest` fallback + fidelity marking
- `capture-core`: event model, redaction engine, deterministic step generator,
  IndexedDB storage, observable store
- Client-side redaction including pre-encode video blur
- LLM enrichment via internal gateway, localhost OpenAI-compatible server, or
  `claude` CLI over native messaging
- Local viewer: timeline playback, step-to-video linking, `⌘K` palette
- Routing: GitHub Issues (device flow), then GitLab Issues (PKCE)
- Export: markdown + `.htr` bundle
- Recording Link page, export-only handoff
- Storage budget with refuse-to-record at cap

### Phase 1 — local-first sync

- `sync-gateway`, `capture-api`, object storage on Nutanix
- Mutation queue, delta pull, LWW per field
- SSO, workspaces, RBAC, projects mapped to brand surfaces
- Share links: signed, expiring, revocable, no-login viewing
- Slack integration; JS SDK with `metadata()`
- `redaction-audit` service (alerting)
- Retention windows and audit log
- LRU eviction of synced captures unlocks

### Phase 2 — agents and external capture

- Server-backed Recording Links (gated on `TRD-SEC-003`)
- MCP server, read-only
- Webhooks via transactional outbox
- CLI for agent-submitted captures

### Phase 3 — mobile and analytics

- iOS ReplayKit capture
- Recurring-bug pattern detection across brand surfaces
- Helpdesk plugin

## 20. Deviations from the PRD

| PRD | This spec | Why |
|---|---|---|
| §6 implies server-side redaction | Redaction is client-side and primary; server pass is an alarm | Phase 0 has no server. And once blur must be pre-encode, client-side is the only correct place. |
| §5.4, §9 — GitLab Issues as the tracker | GitHub Issues first, GitLab second | Directed. Two implementations validate the provider abstraction immediately. |
| §9 — Recording Links in P1 | Export-only in P0, server-backed in P2 | Server-backed Recording Links require a server. |
| §6 — server mints capture identity | Client mints ULIDs | Local-first: a capture must be valid before any server knows of it. |
| §6 — AI summary as async server worker | Client-side, pluggable provider, with local-model paths | Phase 0 has no worker. Local models also keep PHI-adjacent text on-device. |
| §5.3 — AI generates the report | Deterministic generation always; LLM enriches and is validated against the timeline | An LLM alone will invent steps. Citation validation is mandatory. |

## 21. Resolved questions

| Question | Resolution |
|---|---|
| PRD §10.1 — storage and CDN | Self-hosted S3-compatible on Nutanix; signed URLs, no external CDN ([ADR-011](../../decisions/ADR-011-self-hosted-object-storage.md)) |
| PRD §10.2 — LLM gateway or direct | Internal gateway as default, plus localhost and CLI providers ([ADR-007](../../decisions/ADR-007-pluggable-llm-providers.md)) |
| PRD §10.3 — extension distribution | Both: enterprise policy push internally, unlisted Chrome Web Store for contractors and external QA ([ADR-010](../../decisions/ADR-010-extension-distribution.md)) |
| PRD §10.4 — redaction ruleset ownership | Security/Compliance owns ruleset contents; Engineering owns enforcement and test corpus |
| Recording Links vs no-server P0 | Two-stage: export-only P0, server-backed P2 ([ADR-013](../../decisions/ADR-013-recording-links-two-stage.md)) |
| AI summary with no backend | Client-side pluggable providers, deterministic floor ([ADR-007](../../decisions/ADR-007-pluggable-llm-providers.md)) |
| Tracker auth with no backend | Secretless OAuth: GitHub device flow, GitLab PKCE ([ADR-008](../../decisions/ADR-008-tracker-provider-abstraction.md)) |

## 22. Open questions

These do not block Phase 0 planning. Each names who decides and when.

1. **Native-messaging host distribution.** The `claude` CLI bridge needs a host
   manifest and binary installed per machine. Bundle it with the existing dev-machine
   provisioning, or ship a separate installer? *Owner: Platform/IT. Needed before the
   local-LLM path leaves internal beta.*
2. **Internal LLM gateway browser support.** The gateway must accept browser-origin
   CORS and per-user SSO tokens for direct client calls. *Owner: whoever owns the
   gateway. Needed before Phase 0 LLM enrichment ships; the deterministic floor means
   Phase 0 is not blocked on it.*
3. **Video blur CPU budget on QA hardware.** Canvas compositing cost on the lowest
   spec machine in the QA fleet is unmeasured. If it consistently misses frame
   budget, screenshot-only becomes the default there. *Owner: Eng. Measure during
   Phase 0 implementation.*
4. **Retention default.** Per-workspace windows are configurable, but the default
   value is a compliance judgment. *Owner: Security/Compliance. Needed for Phase 1.*

## 23. Success metrics

Carried from PRD §8:

- Share of bug reports filed via capture vs. manual — target >80% within two
  quarters of rollout.
- Median engineer time-to-repro — target 50% reduction.
- Share of captures routed to a tracker with no manual re-entry.
- Brand-surface adoption — all 13+ brands using Recording Links for
  customer-reported issues within two quarters of the Phase 2 release.

Phase-0-specific leading indicator: share of captures where an engineer resolved the
bug without asking the reporter a follow-up question. That is the thesis in
[§1](#1-product-thesis), measured directly.

## 24. Repository layout

```text
clients/
  extension/          MV3 extension (TS)
  viewer/             capture viewer — used by extension and web
  recording-link/     no-login capture page
packages/
  capture-core/       event model, redaction, step generator, storage, store
  llm/                provider interface + HTTP and native-messaging impls
  trackers/           provider interface + GitHub, GitLab, Slack impls
cli/                  agent-facing CLI (Phase 2)
services/             Go services (Phase 1+)
docs/
  decisions/          ADRs
  superpowers/specs/  design specs
```

`capture-core` depends on nothing in `clients/`. That direction is enforced in CI —
it is what keeps three capture surfaces sharing one redaction engine instead of
drifting into three subtly different ones.
