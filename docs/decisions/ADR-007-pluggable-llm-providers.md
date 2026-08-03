# ADR-007: Pluggable LLM providers over a mandatory deterministic floor

## Status

Accepted

## Date

2026-08-04

## Context

PRD-JAMCLONE-001 §5.3 identifies AI-generated titles, summaries, and repro steps as
the highest-leverage feature for reducing QA reporting friction, and places it in the
MVP. It proposes an async server-side worker calling an LLM.

Phase 0 has no server (ADR-002), so the call must originate from the client. That
raises three problems the PRD does not address:

1. **The internal LLM gateway may not accept browser-origin calls.** It needs CORS
   configuration and per-user SSO token handling. That is someone else's
   infrastructure and someone else's timeline.
2. **Capture text may contain PHI that survived redaction.** Redaction is
   pattern-based and will have gaps. Sending capture text to any remote endpoint —
   even an internal one — means PHI-adjacent data leaves the machine. For the
   highest-sensitivity brand surfaces that may be unacceptable regardless of who
   operates the endpoint.
3. **An LLM will invent repro steps.** Asked to produce steps from a noisy timeline,
   a model will confidently emit plausible steps that did not happen. A confidently
   wrong repro document is worse than no document, because an engineer acts on it.

Separately, the user asked specifically for the ability to invoke a local LLM,
including the `claude` CLI. A browser extension cannot exec a binary — that requires
either an HTTP listener on localhost or Chrome's native messaging.

## Decision

**A deterministic step generator always runs. LLM enrichment is optional, pluggable,
and validated against the timeline.**

### The floor: deterministic generation

Every capture gets steps derived mechanically from interaction and navigation events,
with no model involved:

- Noise reduction — scroll bursts coalesce, `mousemove` is dropped, repeated
  identical clicks fold into "clicked N times".
- Target naming by priority: accessible name → associated label → trimmed text
  content → `data-testid` → CSS selector.
- Output: `Clicked "Save changes" on /patients/123/edit`.

This works offline, needs no gateway, and is what makes Phase 0 independent of
infrastructure we do not control.

### The enrichment: two transports, one interface

```ts
interface LlmProvider {
  enrich(input: EnrichmentInput): Promise<ReplicationDoc>;
}
```

| Implementation | Transport | Serves |
|---|---|---|
| `HttpProvider` | `fetch` to an OpenAI-compatible or gateway endpoint | Internal LLM gateway (default) **and** any localhost server — Ollama, LM Studio, litellm — by swapping base URL |
| `NativeMessagingProvider` | `chrome.runtime.connectNative` → host binary → `claude -p` | Local Claude CLI, no HTTP listener required |

Two implementations, not three. The internal gateway and a localhost model server are
the same transport with different configuration, so they share one implementation.

`NativeMessagingProvider` messages are size-bounded: Chrome caps a single native
message at 1 MiB each direction. Capture context sent to the host is truncated to
that budget (deterministic steps plus the highest-signal timeline excerpt, not the
full raw timeline), and a model response that would exceed it is rejected rather
than silently truncated — that failure is non-fatal per the rule below, so it just
falls through to the deterministic document.

Provider selection is policy-controlled per workspace with an ordered fallback
chain, and **the chain never crosses a workspace's declared privacy boundary.** A
workspace configured local-only (brand surfaces where capture text may carry
PHI that survived redaction — see problem 2 above) allow-lists only
`NativeMessagingProvider` and localhost-configured `HttpProvider` targets; if every
allow-listed provider fails, the fallback is the deterministic document, never a
step up to the remote gateway. Falling through to a remote endpoint after a local
failure would silently cross the boundary the workspace was configured to enforce.

### Hallucination control

**Deterministic steps are always kept, unconditionally.** LLM enrichment can only
add to that floor, never replace it.

An LLM-authored step must satisfy two things, not one: its cited `eventIds` must
exist in the capture timeline, **and** the step's text must be about what those
events actually contain — a step describing a network failure needs a cited
network event whose payload shows a failure, not an unrelated event that merely
exists nearby in time. Validation is the citation-existence check plus a
same-provider follow-up prompt that asks the model to justify each step against
only its cited events' payloads; a step that fails either check is dropped, not
flagged. **There is no partial-failure threshold** — every LLM step is validated
independently, and if none survive, the LLM pass is discarded entirely and the
deterministic document stands alone. (The earlier "more than half" threshold is
removed: it permitted exactly half of a document's steps to be fabricated and
still ship.)

### Failure is always non-fatal

Any provider failure leaves the capture `ready` with deterministic steps. AI is
convenience; redaction is compliance. Only the latter blocks (ADR-005).

## Alternatives Considered

### Internal LLM gateway only, LLM output as the sole generator

- Pros: One code path. Centralised key management, cost attribution, and audit
  logging — all genuinely valuable under ISO 27001. Matches the PRD.
- Cons: Phase 0 becomes blocked on gateway CORS and SSO work owned by another team.
  Every capture's text leaves the machine, with no configuration in which it does
  not. And with no deterministic floor, a gateway outage means captures have no steps
  at all.
- Rejected: Single points of failure in both infrastructure and correctness.

### Direct Claude API from the client, user-supplied key

- Pros: Fewest moving parts. No gateway dependency.
- Cons: An API key in extension storage on every QA machine, with no central
  revocation and no audit trail of what capture data left the network. Under ISO 27001
  that is the wrong shape.
- Rejected: The gateway exists precisely to avoid this.

### Defer all AI to Phase 1

- Pros: Phase 0 is simpler and fully offline. Deterministic steps are genuinely
  useful on their own.
- Cons: Drops the PRD's explicitly highest-leverage feature out of the MVP, so the
  MVP cannot test whether AI-generated summaries actually change reporting behaviour
  — which is one of the main things Phase 0 exists to learn.
- Rejected: The deterministic floor gives us the safety this option was buying,
  without giving up the experiment.

### Localhost HTTP only for local models, no native messaging

- Pros: One transport. `HttpProvider` covers Ollama with zero additional code.
- Cons: Does not cover the `claude` CLI, which was explicitly requested and has no
  HTTP server mode. Requiring users to run a separate wrapper daemon shifts the
  problem rather than solving it.
- Rejected: Native messaging is Chrome's purpose-built mechanism for exactly this —
  no port to conflict, no CORS, no listener exposed to other local processes.

### LLM output with post-hoc human review instead of citation validation

- Pros: A human catches subtler errors than an ID check can.
- Cons: Reintroduces the manual write-up friction the product exists to remove, and
  in practice review queues get rubber-stamped.
- Rejected: Citation validation is automatic and catches the specific failure mode
  that matters — fabricated steps.

## Consequences

- **Local model paths are a compliance feature, not a convenience.** They are the
  only configuration in which capture text never leaves the machine. For the
  highest-sensitivity brand surfaces this may become the mandated configuration, which
  means the local paths need to be first-class and tested, not a hobbyist option.
- **The native messaging host needs per-machine distribution** — a host manifest plus
  a binary registered with Chrome. Whether that rides along with existing dev-machine
  provisioning or ships as a separate installer is open (spec §22) and owned by
  Platform/IT.
- **Two prompt-shape targets.** A frontier model via the gateway and a small local
  model have materially different instruction-following ability. The provider
  interface must allow per-provider prompt templates, and the citation validator is
  what keeps a weak local model from degrading document trustworthiness.
- **`generatorModel` is recorded on every document.** Provenance matters when
  triaging a document that reads oddly, and it is what makes it possible to compare
  local-model output quality against the gateway empirically.
- **Citation validation can be over-aggressive.** A genuinely good step whose events
  were dropped by redaction (ADR-005) cites nothing valid and gets discarded. Accepted:
  a step with no surviving evidence is a step an engineer cannot verify.
- **The gateway remains the default** and the recommended configuration, so its CORS
  and SSO support is still needed — just not on the critical path for Phase 0
  shipping.
