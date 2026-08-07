# How to Replicate

A bug-capture tool that turns a screen recording plus ambient browser state into a
**How to Replicate document** — ordered repro steps, each linked to the console,
network, and interaction events that prove it happened, and to the exact video
timestamp where it is visible.

The recording is the input. The document is the product.

## Status

**Phase 0 implemented.** The MV3 extension, the viewer, the no-login Recording Link,
and the three shared packages are built and tested — 616 unit tests plus an
end-to-end suite that drives a real Chromium, loads the real extension, and asserts
the resulting document. No server, per [ADR-002](docs/decisions/ADR-002-client-only-phase-0.md).

- [Design spec](docs/superpowers/specs/2026-08-04-how-to-replicate-design.md) — all four phases
- [Architecture Decision Records](docs/decisions/README.md) — 14 ADRs, with rejected alternatives
- [End-to-end evidence](#end-to-end-evidence) — what actually runs, and the recordings of it

Derived from `PRD-JAMCLONE-001`. Where the spec and the PRD disagree, the spec wins
and the disagreement is listed in spec §20.

## Architecture in one page

Two root decisions shape everything else:

**[ADR-001](docs/decisions/ADR-001-local-first-architecture.md) — the client is the
source of truth, permanently.** IndexedDB holds the workspace; the UI reads an
in-memory observable store hydrated from it. When the server arrives in Phase 1 it is
a *sync target*, never the thing the UI reads from. Mutations apply locally and
synchronously, then sync in the background.

**[ADR-002](docs/decisions/ADR-002-client-only-phase-0.md) — Phase 0 ships zero new
server-side code.** Not "no network": the client calls APIs that already exist
(GitHub, GitLab, the internal LLM gateway, a local model). It means we operate no
service of our own, and no new place where capture data comes to rest under our
control.

Downstream of those:

| Concern | Decision |
|---|---|
| Identity | Client-minted ULIDs — a capture is valid before any server knows of it ([003](docs/decisions/ADR-003-client-minted-ulids.md)) |
| Event model | One append-only timeline for console, network, interaction, navigation, lifecycle, and annotation events ([004](docs/decisions/ADR-004-append-only-event-timeline.md)) |
| Redaction | Client-side, primary, fail-closed. Video blur composited **pre-encode** ([005](docs/decisions/ADR-005-fail-closed-redaction-gate.md)) |
| Capture | CDP primary, marked fallback on DevTools detach ([006](docs/decisions/ADR-006-client-side-capture-via-cdp.md)) |
| Documents | Deterministic generation always; LLM enrichment validated against the timeline ([007](docs/decisions/ADR-007-pluggable-llm-providers.md)) |
| Routing | GitHub device flow, GitLab PKCE — no client secret ships ([008](docs/decisions/ADR-008-tracker-provider-abstraction.md)) |
| Storage | Explicit budget; refuse-to-record rather than evict the only copy ([009](docs/decisions/ADR-009-local-storage-budget.md)) |
| Sync (P1) | Mutation queue, delta pull, last-write-wins per field. No CRDT ([012](docs/decisions/ADR-012-sync-protocol.md)) |

## Invariants

These are not style preferences. Breaking one is a defect, and in two cases a
compliance incident.

1. **No unredacted capture is ever viewable, exportable, or routable.** Export,
   share, route, and MCP read all require `state === 'ready'`. Redaction failure means
   `failed`, not "surface it anyway".
2. **No unblurred frame exists in a persisted artifact.** Blur is composited onto the
   canvas that feeds `MediaRecorder`. If the compositor misses frame budget, the
   capture degrades to screenshot-only — it never emits unblurred video.
3. **Every LLM-authored step cites event IDs that exist.** Steps citing unknown IDs
   are dropped, not flagged. An engineer must be able to trust that every step is
   backed by a recorded event.
4. **Lost fidelity is visible.** `fidelity: 'degraded'` and `withheldEventCount`
   render prominently. A quietly incomplete document that looks complete is worse
   than an obviously incomplete one.
5. **`packages/capture-core` depends on nothing in `clients/`.** Enforced in CI. This
   is what keeps three capture surfaces sharing one redaction engine.

## Layout

```text
clients/
  extension/          MV3 extension (TS) — service worker, content script, offscreen recorder
  viewer/             capture viewer (React) — shared by extension and web
  recording-link/     no-login capture page, export-only
packages/
  capture-core/       event model, redaction, step generator, storage, store
  llm/                provider interface + HTTP and native-messaging impls
  trackers/           provider interface + GitHub, GitLab impls
e2e/                  Playwright end-to-end suite (PR-gating) + recorded evidence
e2e-nightly/          Playwright video-blur OCR job (nightly, slow)
cli/                  agent-facing CLI (Phase 2 — not built)
proto/                Buf module — API schema, generated Go checked in under proto/gen
services/             Go workspace (Phase 1+)
  internal/           shared packages: db, migrate, otel, httpx, storage, authz, testsupport
docs/
  decisions/          ADRs
  superpowers/specs/  design specs
```

## Phases

| Phase | Scope |
|---|---|
| **0** | Extension capture, client redaction, replication documents, GitHub/GitLab routing, local viewer, export-only Recording Link. No server. |
| **1** | Sync engine, object storage, SSO/workspaces/RBAC, share links, Slack, JS SDK. |
| **2** | Server-backed Recording Links, read-only MCP server, webhooks, CLI. |
| **3** | iOS capture, recurring-bug analytics, helpdesk plugin. |

## Setup

Requires **Node 24+** and **pnpm 11+** (the repo pins `pnpm@11.20.0` via `packageManager`).

```bash
pnpm install
```

For the end-to-end suite, also install the browser it drives:

```bash
pnpm exec playwright install --with-deps chromium
```

## Commands

| Command | What it does |
|---|---|
| `pnpm build` | Builds all three clients into their `dist/` folders |
| `pnpm test` | Unit tests (616, Vitest) |
| `pnpm test:coverage` | Unit tests with coverage thresholds enforced |
| `pnpm e2e` | Builds, then runs the Playwright end-to-end suite in real Chromium |
| `pnpm e2e:report` | Opens the HTML report from the last e2e run |
| `pnpm e2e:nightly` | Runs the video-blur OCR job (slow — records, decodes, and OCRs real video) |
| `pnpm lint` | ESLint over packages, clients, e2e, and e2e-nightly |
| `pnpm typecheck` | `tsc -b` over the workspace, plus the e2e and e2e-nightly projects |
| `pnpm check:deps` | Enforces invariant 5 — `capture-core` imports nothing from `clients/` |

## Go services (Phase 1+)

Requires **Go 1.25+**, the [buf CLI](https://buf.build/docs/installation), and Docker
(for `testcontainers-go`-backed integration tests). `go.work` at the repo root covers
`services/*` and `proto` (the generated proto Go module); `services/internal` is the
shared package every service builds inside — no service has landed yet.

### Schema and wire contract (Task 3)

The Postgres schema lives in `services/internal/migrate/migrations/00002_schema.sql`:
`workspaces`/`projects`/`users`/`memberships`, `captures` (carrying
`applied_ruleset_version` — the ruleset the *client* redacted with, distinct from
whatever `redaction-audit` evaluates against later), append-only `capture_events`
(`t` is a ms offset from `capture.epoch`, never wall-clock), `assets`, `mutations`
(PK on the client-minted mutation ULID, so replay is idempotent), `comments`,
versioned `redaction_rulesets`, append-only `audit_log`, `share_links`,
`integration_bindings`, and `outbox` (table only — Phase 2 wires the logic).

Two Postgres roles enforce immutability, not just convention: the **migration role**
(whoever runs `goose`, i.e. owns the schema) and **`htr_runtime`**, the role every
service connects as, holding `SELECT`+`INSERT` on the append-only tables and full
CRUD only on tables genuinely mutated in place. `services/internal/db.WithRuntimeRole`
rewrites an admin DSN into the runtime role's DSN. A `BEFORE UPDATE OR DELETE` trigger
on every append-only table is defence in depth on top of the grant — it rejects the
same statements even for the schema owner, who bypasses grants.

The wire contract is `proto/sync/v1/sync.proto` (the closed `Mutation` oneof —
`SetTitle`/`SetSummary`/`SetTags`/`Assign`/`AppendComment` — plus `SyncService`) and
`proto/capture/v1/capture.proto` (`CaptureService`, the read surface). Both compile to
type-safe Go via `sqlc` (`services/sqlc.yaml` → `services/internal/db/sqlcgen`) and
buf/connect-go (`proto/buf.gen.yaml` → `proto/gen/go`). This task delivers schema,
protos, and generated code only — no service wires them up yet (Tasks 4+).

```bash
go work sync                       # sync go.work with each module's go.mod
cd services/internal && go test ./...   # unit + integration tests (docker required for
                                         # the testcontainers-backed tests in migrate/
                                         # and storage/storage_integration_test.go —
                                         # they skip cleanly if no daemon is reachable)
golangci-lint run ./...            # from services/internal
```

`services/internal/testsupport` is a docker-backed test harness (Postgres + MinIO via
testcontainers-go) with no business logic of its own; its coverage is inherently
environment-dependent, so CI's 100% coverage gate excludes that one package and covers
everything else.

Proto (Buf module, `proto/`):

```bash
cd proto
buf lint
buf breaking --against '.git#branch=main,subdir=proto'
buf generate    # regenerates checked-in Go under proto/gen — CI fails on drift
```

`sqlc.yaml` lives at `services/sqlc.yaml`; once queries exist under
`services/internal/db/queries`, run `sqlc generate` from `services/` to produce
type-safe Go under `services/internal/db/sqlcgen`. Migrations are plain SQL run via
`goose` (`services/internal/migrate`), embedded at build time.

## Running the extension

```bash
pnpm build
```

Then load it unpacked:

1. Open `chrome://extensions` and turn on **Developer mode**.
2. **Load unpacked** → select `clients/extension/dist`.
3. Click the toolbar icon on the tab you want to capture; click it again to stop.

**The extension will refuse to capture until a redaction ruleset is present.** That is
invariant 1 working, not a bug. The ruleset is read from `chrome.storage.managed`
(enterprise policy, spec §4.2) under the key `ruleset`; with no managed policy,
`start()` returns `null` before any `chrome.debugger.attach` call — an unlisted origin
gets zero bytes of anything. Deploy a managed policy matching `managed_schema.json`
to enable capture.

The viewer and the Recording Link are ordinary static bundles — serve
`clients/viewer/dist` or `clients/recording-link/dist` from any static server. The
viewer reads captures from IndexedDB on its own origin.

## End-to-end evidence

`pnpm e2e` runs four specs against a real Chromium — no jsdom, no mocked browser —
and records a video of each. The committed recordings live in
[`e2e/artifacts/videos/`](e2e/artifacts/videos) so the evidence has a stable path; CI
re-runs the suite on every push and uploads that run's recordings as the
`e2e-evidence` artifact.

A local run writes its videos to the gitignored `e2e/artifacts/test-results/` and
leaves the committed ones alone, so testing does not dirty the tree. To deliberately
re-cut the committed evidence, run `HTR_REFRESH_EVIDENCE=1 pnpm e2e`.

| Spec | What it proves |
|---|---|
| `pipeline.spec.ts` — full pipeline | Real clicks, typing, a real failing `fetch`, and a real uncaught exception on a demo clinic app become a redacted How to Replicate document, rendered in the real viewer. Asserts the planted PHI is absent from the persisted events **and** that the engine actually fired (`[REDACTED:mrn]` markers, rules recorded as applied) — so "no PHI" cannot pass vacuously. Clicking each step highlights exactly the timeline rows for the event IDs it cites (invariant 3, in the DOM). Drives the ⌘K palette. |
| `pipeline.spec.ts` — invariant 4 | A legacy endpoint whose body shape defeats a `field-path` rule makes the engine fail closed and drop the event. Asserts the dropped request is not persisted at all, the capture lands `degraded`, and the viewer says so. |
| `gate.spec.ts` — invariant 1 | Captures seeded in `failed` and `composing` render none of their document — not the title, summary, steps, timeline, player, nor the fidelity badge — and never appear in the palette. Asserted against unique marker strings, not element absence. |
| `extension.spec.ts` | The real MV3 extension loads into real Chrome, its service worker boots, its content script injects, and a real click travels content script → service worker as a real `CaptureEvent`. Emits nothing before a session starts, and stops emitting after one ends. |
| `service-worker.spec.ts` — off-allow-list refusal | `createServiceWorker` (production code) refuses to start a capture for an origin not on the loaded ruleset — no `chrome.debugger.attach` call happens at all. |
| `service-worker.spec.ts` — CDP-detach handoff | A real CDP session backing an active capture is detached mid-capture; the production `onDetach` handler flips fidelity to `degraded` and records the handoff as a `lifecycle` event. |

Events reach the pipeline through a real CDP session (`Network`, `Runtime`, `Console`
domains), mapped by the extension's own `mapCdpEvent`. Playwright stands in for
`chrome.debugger` at that one seam; everything downstream is product code.

The redacted console and network events those two pipeline specs persist are dumped
as committed JSON evidence alongside the videos:
[`e2e/artifacts/events/full-pipeline.json`](e2e/artifacts/events/full-pipeline.json)
and
[`invariant-4-degraded.json`](e2e/artifacts/events/invariant-4-degraded.json). They
show the real captured console error and `POST /api/patient-chart` request/response —
with the synthetic PHI in the URL, headers, and body replaced by
`[REDACTED:*]`/`[REDACTED]` and the exact redaction rules that fired recorded per
event.

### service-worker.spec.ts: how it gets a real ruleset without a real policy

`extension.spec.ts` loads the real unpacked extension via `launchPersistentContext`,
but the with-ruleset branch of `serviceWorker.start()` (off-allow-list refusal,
CDP-detach handoff) needs a non-empty `chrome.storage.managed` — and managed policy
storage is only populated by an OS-level enterprise policy file, unavailable to the
vanilla Chromium binary Playwright drives. `service-worker.spec.ts` instead imports
`createServiceWorker` directly (the same production module `clients/extension/src/
background/main.ts` wires against the real `chrome` global) and drives it against a
real Playwright page and a real CDP session for `chrome.debugger`; only the handful
of Chrome namespaces unreachable outside a loaded extension (`storage.managed`,
`tabs`, `action`, `webRequest`) are stubbed, same as `service-worker.test.ts`'s unit
tests. The CDP session really attaches and really detaches; only the delivery of
Chrome's own `onDetach` event (which nothing outside a loaded extension can trigger)
is invoked directly, immediately after that real detach.

### What the e2e does not cover

Stated plainly, because a test suite that overstates itself is worse than a small one:

- **Video capture via `getDisplayMedia` (invariant 2).** It needs a screen-picker
  grant that a headless CI run cannot give. The frame-budget monitor and
  screenshot-only degrade path are unit-tested only. (The blur compositor itself —
  the part that actually removes PHI from a frame — is covered end-to-end by the
  nightly job below.)
- **The toolbar-click path in a real browser.** `chrome.action.onClicked` cannot be
  synthesized from a test. `extension.spec.ts` asserts the fail-closed side (no
  ruleset, no capture); `service-worker.spec.ts` covers the with-ruleset branches
  (see above) by driving `createServiceWorker` directly instead of the toolbar.
- **`navigation` events.** Nothing in `clients/` emits them yet even though
  `generateDoc` renders them; the e2e harness mints them from a real `hashchange`.
  A real gap, not a test shortcut.
- **LLM enrichment and issue routing.** `packages/llm` and `packages/trackers` are
  unit-tested against recorded fixtures; the e2e exercises the deterministic
  document floor only.

## Nightly video-blur OCR job

The redaction corpus (`packages/capture-core/test/redaction-corpus.test.ts`) gates
every PR but only covers text-shaped PHI — JSON bodies, headers, DOM text. Video
frames are the one place PHI can leak in pixels rather than characters, and OCR-ing
real recorded video is slow (multiple seconds per frame), so it runs as its own
nightly workflow ([`.github/workflows/nightly-blur.yml`](.github/workflows/nightly-blur.yml))
instead of gating every PR — run it locally with `pnpm e2e:nightly`.

`e2e-nightly/fixture/entry.ts` draws known synthetic PHI text onto a canvas, blurs it
through the real pre-encode blur code (`compositeFrame`/`validateRegions` from
`@htr/capture-core`, the same functions the offscreen recorder drives), records the
canvas's own `MediaStream` with a real `MediaRecorder` into an actual `video/webm`
blob, and decodes stills back out of that recording through a real `<video>` element
— encode, then decode, the same round trip a persisted capture goes through.
`e2e-nightly/blur-ocr.spec.ts` then OCRs (`tesseract.js`) the decoded stills and fails
the job if the PHI text is recovered from any of them. It also OCRs one frame from
the same pipeline with blurring disabled and asserts the PHI text *is* recovered
there — proof the "not found" assertion on the blurred frames isn't vacuous. A match
fails the job outright; nothing here is `continue-on-error`.

## Contributing

Read [ADR-001](docs/decisions/ADR-001-local-first-architecture.md) and
[ADR-002](docs/decisions/ADR-002-client-only-phase-0.md) first — every other decision
is downstream of one or both.

- Significant architectural decisions get an ADR. Follow the existing format and
  continue the sequence; don't restart numbering.
- The redaction golden corpus (spec §18) is a hard CI gate. A leak fails the build.
  It is reviewed by Security/Compliance, not Eng alone.
- 100% coverage of branches and edge cases.
