# Implementation plans

One plan per delivery phase from
[the design spec](../specs/2026-08-04-how-to-replicate-design.md) §19. Each plan
decomposes its phase into numbered tasks and carries a **Global Constraints**
section that binds every task in it.

| Plan | Phase | Tasks | Depends on |
|---|---|---|---|
| [Phase 0](2026-08-04-phase-0-implementation.md) | client only, zero new server-side code | 14 | — |
| [Phase 1](2026-08-04-phase-1-implementation.md) | local-first sync | 15 | Phase 0 |
| [Phase 2](2026-08-04-phase-2-implementation.md) | agents and external capture | 7 | Phase 1 |
| [Phase 3](2026-08-04-phase-3-implementation.md) | mobile and analytics | 5 | Phase 2 |

## How to read a plan

Read, in this order:

1. The plan's **Global Constraints** section.
2. **Your one task.** Not the other tasks — a task brief is written to stand alone.
3. Anything your task **names**: a dependency plan's task it builds on, a shared
   asset it must agree with (the redaction corpus, the golden timeline fixtures),
   or an ADR it cites.

Stop there. The point of (2) is that you should not need to read a whole plan to
implement one task — not that upstream context is off limits. Several tasks
depend on agreeing exactly with work from an earlier phase, and the conformance
suites only mean something if you have read what you are conforming to.

Exact values — thresholds, formats, magic strings — live in the task text and are
meant to be used verbatim.

## Invariants accumulate

The five invariants in [CLAUDE.md](../../../CLAUDE.md) bind every phase. Each
plan adds its own and restates the earlier ones, so the list in the newest plan
is the complete one:

| # | Added by | Invariant |
|---|---|---|
| 1–5 | Phase 0 | ready gate · pre-encode blur · LLM citation validation · visible fidelity loss · `capture-core` depends on nothing in `clients/` |
| 6–9 | Phase 1 | server is never the read path · `redaction-audit` is an alarm · eviction requires `manifestComplete` · nothing unredacted is transmitted |
| 10–14 | Phase 2 | `TRD-SEC-003` gates anonymous upload · anonymous uploads inspected in a non-durable buffer and rejected on failure · MCP is read-only · every MCP read is audited · one retry-and-backoff implementation |
| 15–18 | Phase 3 | iOS is degraded by definition · on-device redaction, same ruleset format · pattern detection is not session-replay analytics · no native Android |

## Decisions these plans make

The spec left implementation-level choices open. The plans close them in their
Stack decisions tables — pnpm/TypeScript/Vitest/Vite/React for the clients, and
Go with chi, ConnectRPC, Buf, `pgx`, `sqlc`, `goose`, and `testcontainers-go`
for the services. Those are settled the same way the ADRs are: change them
through a new decision, not inside a task.

Architectural decisions belong in [docs/decisions/](../../decisions/README.md),
not here.

## Tasks that must not start silently

Three tasks have a prerequisite that is somebody else's to grant. Each says to
report `BLOCKED` rather than proceed:

- **Phase 2 Task 4** — server-backed Recording Links, gated on `TRD-SEC-003`.
- **Phase 2 Task 7** — native-messaging host distribution, owned by Platform/IT.
- **Phase 3 Task 5** — helpdesk plugin, pending the choice of helpdesk product.

Phase 1 Task 13 has a softer version of the same: the retention default is a
Security/Compliance judgment, implemented as a required policy value with no
code-level default.
