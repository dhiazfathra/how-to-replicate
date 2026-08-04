# How to Replicate

A bug-capture tool that turns a screen recording plus ambient browser state into a
**How to Replicate document** — ordered repro steps, each linked to the console,
network, and interaction events that prove it happened, and to the exact video
timestamp where it is visible.

The recording is the input. The document is the product.

## Status

**Phase 0 implementation in progress.** Extension, viewer, recording-link clients and
the capture-core/llm/trackers packages exist with tests; see [Planned
layout](#planned-layout) for what's built.

- [Design spec](docs/superpowers/specs/2026-08-04-how-to-replicate-design.md) — all four phases
- [Architecture Decision Records](docs/decisions/README.md) — 14 ADRs, with rejected alternatives

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

## Planned layout

```text
clients/
  extension/          MV3 extension (TS)
  viewer/             capture viewer — shared by extension and web
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

## Phases

| Phase | Scope |
|---|---|
| **0** | Extension capture, client redaction, replication documents, GitHub/GitLab routing, local viewer, export-only Recording Link. No server. |
| **1** | Sync engine, object storage, SSO/workspaces/RBAC, share links, Slack, JS SDK. |
| **2** | Server-backed Recording Links, read-only MCP server, webhooks, CLI. |
| **3** | iOS capture, recurring-bug analytics, helpdesk plugin. |

## Commands

Requires `pnpm`. From the repo root:

```bash
pnpm install
pnpm lint          # eslint
pnpm typecheck     # tsc -b
pnpm test          # vitest
pnpm test:coverage # vitest with coverage
pnpm check:deps    # enforce capture-core/clients dependency boundary
pnpm build         # build all packages
```

## Contributing

Read [ADR-001](docs/decisions/ADR-001-local-first-architecture.md) and
[ADR-002](docs/decisions/ADR-002-client-only-phase-0.md) first — every other decision
is downstream of one or both.

- Significant architectural decisions get an ADR. Follow the existing format and
  continue the sequence; don't restart numbering.
- The redaction golden corpus (spec §18) is a hard CI gate. A leak fails the build.
  It is reviewed by Security/Compliance, not Eng alone.
- 100% coverage of branches and edge cases.
