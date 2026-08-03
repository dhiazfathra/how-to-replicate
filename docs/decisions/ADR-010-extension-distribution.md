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
| Enterprise policy force-install | Internal QA and engineering on managed browsers | Origin allow-list and ruleset **policy-pinned**, not user-editable |
| Chrome Web Store, unlisted | Contractors, external QA, unmanaged machines | Same defaults, configured in-extension; allow-list still enforced, but from the bundled default rather than policy |

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
- Cons: Cannot pin the origin allow-list, so the security model's central control
  becomes user-editable. Every release — including a redaction ruleset fix, which is
  a compliance fix — waits on Google review. `debugger` permission invites repeated
  review friction.
- Rejected: Making the allow-list user-editable is not acceptable, and putting
  compliance fixes behind third-party review latency is worse.

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
- **Contractors get the weaker configuration.** Documented, and a reason to prefer
  onboarding contractors onto managed browsers where practical.
- Dropping the Web Store channel later is trivial. Adding it later would not have been
  — it requires store registration, review, and a distinct key, all of which are
  slow. Establishing both up front is the cheaper ordering.
