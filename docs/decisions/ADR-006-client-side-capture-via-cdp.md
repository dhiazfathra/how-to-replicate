# ADR-006: Capture console and network via CDP, with a marked fallback

## Status

Accepted

## Date

2026-08-04

## Context

The product's entire value is automatic technical context: console output and
network activity attached to a capture with no user effort. A Chromium extension has
three ways to obtain that data, and they differ sharply in fidelity.

| Mechanism | Console | Request bodies | Response bodies | Pre-injection events |
|---|---|---|---|---|
| `chrome.debugger` (CDP) | full, with stack traces | yes | yes | yes |
| `chrome.webRequest` | no | headers only | no | yes |
| Page-context monkey-patching | yes | yes | yes | **no** |

Only CDP provides response bodies, which are frequently where the actual bug is —
a 200 response carrying an error payload is invisible to `webRequest`.
Monkey-patching `fetch`, `XMLHttpRequest`, and `console` gets bodies but misses
everything before the patch is installed, which in practice means missing the
application bootstrap where a meaningful share of bugs occur.

CDP has one hard constraint: **only one debugger client may attach to a tab at a
time.** If the user opens DevTools, Chrome detaches ours. This is not rare — a QA
engineer capturing a bug is exactly the kind of user who opens DevTools.

Video is separate and less contested. `MediaRecorder` over a canvas stream is the
only viable client-side path, and it is required anyway for pre-encode blur
(ADR-005).

## Decision

**Primary path: `chrome.debugger` attaching to `Network`, `Console`, `Runtime`, and
`Log` CDP domains.**

**Fallback on detach: `chrome.webRequest` for network metadata plus page-context
monkey-patching for console and bodies. The capture is stamped
`fidelity: 'degraded'` and the viewer renders that badge prominently.**

Video: display stream drawn to `OffscreenCanvas`, blur composited, canvas stream fed
to `MediaRecorder` in an offscreen document (MV3 service workers cannot hold media
streams). Chunked timeslice output feeds the Instant Replay ring buffer.

No server-side transcoding, in any phase. Client-side WebM is what gets stored and
played.

The degraded marking is the part that matters most. Silently losing console logs
while presenting a confident, complete-looking replication document is worse than
capturing nothing — an engineer would trust a document that is quietly incomplete
and waste time on a false picture.

## Alternatives Considered

### `chrome.webRequest` only

- Pros: No debugger banner, no detach problem, no conflict with DevTools, lower
  permission surface.
- Cons: No console capture at all, and no response bodies. Removes the two highest-value
  signals — the stack trace and the error payload — leaving little more than a
  waterfall.
- Rejected: Does not deliver the product's core promise.

### Monkey-patching only, injected via an early content script

- Pros: No debugger permission, no detach, works uniformly, cross-browser including
  Firefox and Safari.
- Cons: Misses everything before injection, including bootstrap errors. Patches are
  detectable and defeatable by page code. Fragile against pages that re-wrap
  `fetch` or `console` themselves.
- Rejected as primary; adopted as part of the fallback where its weaknesses are
  disclosed via the fidelity flag.

### CDP only, refuse to capture on detach

- Pros: One code path. Fidelity is uniform and guaranteed. No ambiguity about what a
  capture contains.
- Cons: The failure mode is "you opened DevTools, so you get nothing" — precisely
  when a QA engineer is most actively debugging. Losing captures in the highest-value
  moment is worse than a marked partial capture.
- Rejected: A disclosed partial capture is more useful than no capture.

### Silent fallback, no fidelity flag

- Pros: Simpler UI, no badge to explain, no user confusion about capture tiers.
- Cons: Produces documents that look authoritative but have missing console context.
  An engineer trusts the document, chases the wrong hypothesis, and the tool has
  made things worse than a Slack screenshot would have.
- Rejected: This is the failure mode the product exists to eliminate.

## Consequences

- **`chrome.debugger` shows an infobar** ("… is debugging this browser"). Unavoidable
  and slightly alarming to non-technical reporters. Acceptable for internal QA;
  a reason Recording Links (ADR-013) use `getDisplayMedia` and monkey-patching
  instead — external clinic users get the degraded tier by design.
- **Two capture implementations to maintain and test**, both normalising into the
  single event model (ADR-004). The shared model is what keeps this from becoming two
  divergent products; the normalisation boundary is tested independently of both
  sources.
- **The `debugger` permission is broad** and draws Chrome Web Store review scrutiny —
  a factor in the distribution decision (ADR-010) and an argument for the
  enterprise-policy channel.
- **Detach can occur mid-capture.** The transition must be seamless: the fallback
  takes over, the capture continues, `fidelity` flips to `degraded`, and the timeline
  shows the transition as a `lifecycle` event so a reader can see exactly where
  fidelity changed.
- **Chromium-only for the primary path.** Firefox and Safari have no CDP equivalent.
  Non-Chromium browsers would get the monkey-patching tier only. Out of scope for
  now; the fallback existing means it is not a rewrite.
- **No server transcode means WebM is the delivery format.** Playback works in
  Chromium and Firefox; Safari support for WebM is uneven, which matters when a
  no-login share link is opened by a non-technical stakeholder on an iPhone.
  Accepted for Phase 0; revisit at Phase 1 share links, where a one-time
  server-side transcode for playback compatibility becomes possible — noting it would
  operate on an already-blurred source, so it does not reopen ADR-005.
