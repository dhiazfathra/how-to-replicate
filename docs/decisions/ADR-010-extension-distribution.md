# ADR-010: Distribute the extension by enterprise policy and unlisted Web Store

## Status

Accepted

## Date

2026-08-04

## Context

The extension needs to reach three populations with different trust and management
characteristics:

- **Internal QA and engineering** on managed machines. The primary Phase 0 audience.
- **Contractors and external QA** on machines outside our device management.
- **External clinic customers**, who will not install anything — they are served by
  Recording Links (ADR-013), not the extension.

Chromium offers two delivery mechanisms, and PRD open question #3 asks which.

**Enterprise policy force-install** pushes the extension via `ExtensionSettings`
policy to managed browsers. No user action, no store review, immediate release
cadence, and policy can pin configuration — which matters here because the origin
allow-list and redaction ruleset are policy-controlled (ADR-005) and must not be
user-editable.

**Chrome Web Store, unlisted** means installable by anyone with the link but absent
from search. Reaches unmanaged machines. Every release waits on Google review, and
`debugger` permission (ADR-006) reliably draws extended scrutiny. "Unlisted" is not
private — the URL is guessable in principle and shareable in practice.

## Decision

**Both channels.**

| Channel | Audience | Configuration |
|---|---|---|
| Enterprise policy force-install | Internal QA and engineering on managed browsers | Origin allow-list and ruleset **policy-pinned** — the org sets the value centrally and it is not exposed as a setting anywhere in the extension UI |
| Chrome Web Store, unlisted | Contractors, external QA, unmanaged machines | No policy to pin from, so the extension ships an **immutable bundled default** and refreshes it at runtime over a signed channel (below) — same as the policy channel, neither surface lets the person using the browser edit it |

**In neither channel can the person using the browser edit the allow-list or
ruleset.** The only difference is who controls the value that ships: an org
administrator via policy, or the extension's own signed default-and-refresh
mechanism when no policy exists to pin it. There is no third, user-facing path —
if there were, it would defeat the point of pinning it at all.

External clinic customers are explicitly **not** a target for either channel. They
use Recording Links.

The policy channel is the primary one, and the one the security model assumes. The
Web Store channel exists to cover machines we do not manage, and it is understood to
be the weaker configuration.

## Alternatives Considered

### Enterprise policy only

- Pros: Single channel. Strongest security posture — every install has a pinned
  allow-list and ruleset. No store review, so no release latency and no scrutiny of
  the `debugger` permission. Nothing publicly fetchable.
- Cons: Excludes contractors and external QA entirely. Those users then either go
  without the tool or resort to the Recording Link export flow, which is a
  materially worse experience for someone doing QA work daily.
- Rejected on coverage. It remains the preferred channel wherever it is available.

### Unlisted Web Store only

- Pros: One channel covering everyone. Standard update mechanism. No policy
  infrastructure needed.
- Cons: Cannot pin the origin allow-list via org policy — there is no administrator
  channel setting it centrally, only the extension's own bundled default. Every
  *store release* — including a redaction ruleset fix, which is a compliance fix —
  would wait on Google review if the ruleset shipped only via extension update.
  `debugger` permission invites repeated review friction.
- Rejected: Losing the policy-pinning coverage entirely is not acceptable for the
  primary internal audience, and putting compliance fixes behind third-party
  review latency — rather than the runtime-fetch mechanism this ADR adopts below —
  is worse.

### Self-hosted `.crx` with an update URL

- Pros: Full release control, no store review, no policy infrastructure.
- Cons: Chromium blocks off-store extension installs on Windows and macOS unless
  force-installed by policy — which reduces this to the enterprise-policy option with
  extra hosting work. Also requires operating signing key infrastructure.
- Rejected: No coverage advantage over policy push, plus additional burden.

### Published (listed) on the Web Store

- Pros: Discoverable. Conventional install path. Would matter if this were externalised
  as a product.
- Cons: A tool whose purpose is capturing internal application state has no reason to
  be publicly discoverable. Invites installs from people with no legitimate use, and
  raises the review bar further.
- Rejected: No benefit for an internal tool.

## Consequences

- **Two build artifacts and two release processes.** The Web Store build waits on
  review; the policy build ships when we say. Version skew between the two
  populations is normal, so the ruleset bundle format needs a version field and
  backward compatibility.
- **The extension ID differs between channels.** A Web Store extension's ID is derived
  from its store-assigned key; a policy-pushed build may use a different key. This
  affects anything keyed on extension identity — notably the native messaging host
  manifest (ADR-007), whose `allowed_origins` must list **both** IDs.
- **Unlisted is not private.** The Web Store build must assume an untrusted installer:
  it enforces the origin allow-list from its bundled default and, absent policy, an
  install outside our organisation simply captures nothing useful. Worth stating
  explicitly — the allow-list is what makes an unlisted build safe to exist, not
  obscurity.
- **Compliance fixes are faster on one channel than the other.** A redaction ruleset
  gap fixed by policy reaches internal users within a browser refresh cycle; the same
  fix reaches contractors after Google review. Mitigated by fetching ruleset bundles
  at runtime rather than embedding them — which makes ruleset fixes independent of
  extension releases on both channels, and is the more important structural point.
- **Runtime ruleset fetching must not become the weaker path.** Faster delivery
  is only safe if the fetched bundle is as trustworthy as a policy-pinned one, so
  the runtime channel is signed and versioned: each bundle carries a monotonic
  version and a signature checked against a trust root pinned in the extension
  binary itself (not fetched, so it can't be substituted alongside a forged
  bundle). Activation is atomic — a bundle is either fully adopted or not adopted
  at all, never partially applied. If a fetch fails, is unreachable, or fails
  signature verification, the extension keeps the last-known-good bundle already
  active; if there is no last-known-good bundle yet (first run with no policy and
  no successful fetch), it **blocks capture** rather than falling back to no
  ruleset at all. Policy-pinned rules, where present, remain authoritative over
  anything the runtime channel could ever deliver.
- **Contractors get the weaker configuration.** Documented, and a reason to prefer
  onboarding contractors onto managed browsers where practical.
- Dropping the Web Store channel later is trivial. Adding it later would not have been
  — it requires store registration, review, and a distinct key, all of which are
  slow. Establishing both up front is the cheaper ordering.
