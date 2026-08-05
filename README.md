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
e2e/                  Playwright end-to-end suite + recorded evidence
cli/                  agent-facing CLI (Phase 2 — not built)
services/             Go services (Phase 1+ — not built)
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
| `pnpm lint` | ESLint over packages, clients, and e2e |
| `pnpm typecheck` | `tsc -b` over the workspace, plus the e2e project |
| `pnpm check:deps` | Enforces invariant 5 — `capture-core` imports nothing from `clients/` |

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

Events reach the pipeline through a real CDP session (`Network`, `Runtime`, `Console`
domains), mapped by the extension's own `mapCdpEvent`. Playwright stands in for
`chrome.debugger` at that one seam; everything downstream is product code.

### What the e2e does not cover

Stated plainly, because a test suite that overstates itself is worse than a small one:

- **Video capture and pre-encode blur (invariant 2).** `getDisplayMedia` needs a
  screen-picker grant that a headless CI run cannot give. The blur compositor, frame
  budget, and screenshot-only degrade path are unit-tested only.
- **The toolbar-click path in a real browser.** `chrome.action.onClicked` cannot be
  synthesized from a test, and `chrome.storage.managed` needs an enterprise-managed
  profile. The e2e asserts the fail-closed side (no ruleset, no capture); the
  with-ruleset branch of `serviceWorker.start()` is unit-tested.
- **`navigation` events.** Nothing in `clients/` emits them yet even though
  `generateDoc` renders them; the e2e harness mints them from a real `hashchange`.
  A real gap, not a test shortcut.
- **LLM enrichment and issue routing.** `packages/llm` and `packages/trackers` are
  unit-tested against recorded fixtures; the e2e exercises the deterministic
  document floor only.

## Contributing

Read [ADR-001](docs/decisions/ADR-001-local-first-architecture.md) and
[ADR-002](docs/decisions/ADR-002-client-only-phase-0.md) first — every other decision
is downstream of one or both.

- Significant architectural decisions get an ADR. Follow the existing format and
  continue the sequence; don't restart numbering.
- The redaction golden corpus (spec §18) is a hard CI gate. A leak fails the build.
  It is reviewed by Security/Compliance, not Eng alone.
- 100% coverage of branches and edge cases.
