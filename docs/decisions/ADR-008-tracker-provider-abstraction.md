# ADR-008: Tracker provider abstraction — GitHub first, GitLab second, secretless auth

## Status

Accepted

## Date

2026-08-04

## Context

One-click routing into an existing issue tracker is half the product's value. A
capture that requires manual re-entry into a tracker has saved the reporter nothing.

PRD-JAMCLONE-001 names GitLab Issues as the target, reasoning from the ERP stack.
Direction since has changed the priority: **GitHub Issues first, GitLab Issues
second.**

That change is architecturally useful. A provider interface with one implementation
is speculative generality — the interface inevitably leaks the single provider's
assumptions and needs reworking when the second arrives. Two implementations from
the start means the abstraction is validated by use rather than by intention.

Authentication is the harder problem. ADR-002 removes any server from Phase 0, so
there is nowhere to hold a client secret. An extension is a public client: anything
shipped in its bundle is readable by anyone who installs it.

The obvious shortcut — have each user paste a Personal Access Token — fails on
several counts under ISO 27001. A long-lived, broadly-scoped credential ends up in
extension storage on every QA machine. Revocation is manual and per-user. And there
is no way to distinguish the tool's actions from the user's own API activity in
audit logs.

The secretless OAuth paths differ between the two providers, and this is a real
constraint rather than a preference:

- **GitLab** supports OAuth with PKCE for public clients — the `code_verifier`
  fully replaces the client secret in the token exchange, so a public client needs
  no secret at all.
- **GitHub** OAuth Apps accept the PKCE parameters (`code_challenge`,
  `code_challenge_method=S256`, `code_verifier`) since mid-2025, but GitHub still
  does not distinguish public from confidential clients: exchanging the
  authorization code for a token still requires `client_secret` regardless of
  whether PKCE was used. PKCE hardens the GitHub flow against interception; it does
  not remove the secret requirement the way it does for GitLab. So GitHub's actual
  secretless path for a public client remains the **device authorization flow**,
  which needs no client secret and no redirect URI at all.

## Decision

**One provider interface, two implementations, provider-appropriate secretless
OAuth.**

```ts
interface TrackerProvider {
  authorize(): Promise<Session>;
  createIssue(idempotencyKey: string, doc: ReplicationDoc, target: ProjectTarget)
    : Promise<IssueRef>;
  attachAssets(issue: IssueRef, assets: AssetRef[]): Promise<AttachResult[]>;
}
```

`createIssue` and `attachAssets` are separate remote writes and must be treated as
such: `idempotencyKey` is the capture's ULID, and the returned `IssueRef` is
persisted locally **immediately** on success, before `attachAssets` is ever called.
An outbox retry after a partial failure re-reads that persisted `IssueRef` and
retries only the attachment step — it never calls `createIssue` again for a
capture that already has one. `attachAssets` returns a per-asset result so a
single oversized attachment (over a provider's size limit) fails independently:
the issue links back to the capture for that asset instead of blocking the
others.

| Provider | Priority | Flow | Why this flow |
|---|---|---|---|
| GitHub | first | OAuth **device flow** (`/login/device/code`) | GitHub's code exchange requires `client_secret` even with PKCE present. Device flow needs no client secret and no redirect URI — the latter being awkward in an extension anyway. |
| GitLab | second | OAuth **PKCE** | GitLab's PKCE `code_verifier` fully substitutes for a client secret for public clients. |
| Slack | Phase 1 | OAuth PKCE via the Phase 1 server | Not a tracker; shares the outbox dispatch path. |

Token handling:

- Access tokens in `chrome.storage.session` — cleared on browser exit, plaintext is
  acceptable there since the value doesn't survive past the current browser
  session anyway.
- Refresh tokens encrypted at rest in `chrome.storage.local`, because unlike the
  access token this one **does** survive browser restarts and is the higher-value
  target. The encryption key is derived via WebCrypto's `AES-GCM`, generated
  on first authorization and itself stored as a non-extractable `CryptoKey` in
  `chrome.storage.session` — so the key never exists in a form that can be read
  out of storage, copied, or exfiltrated as bytes; it dies with the browser
  session exactly like the access token. This means a refresh token is only ever
  decryptable while the browser session that created it is still open — on
  restart, the encrypted blob in `.local` is unreadable and the user must
  re-authorize. That's a deliberate trade: no durable key material to protect or
  rotate, at the cost of re-authorizing more often than a "silently refresh
  forever" design would.
- Revocation is the tracker's own OAuth revocation endpoint (GitHub/GitLab both
  support token revocation), called on explicit sign-out and offered as an
  org-admin action — revoking there invalidates the token network-side
  regardless of what's cached locally, which is what actually matters for a lost
  or compromised device.
- No credential is ever typed by the user into the extension.
- Both storage areas are reachable by content scripts by default. Neither token
  (nor the wrapping key) needs to be, so the background service worker calls
  `chrome.storage.session.setAccessLevel({ accessLevel: 'TRUSTED_CONTEXTS' })` (and
  the equivalent for `.local`) at startup, restricting both areas to the
  extension's own pages and service worker. A compromised content script on a
  captured page then cannot read either token or the key.

Routing payload: the replication document as issue body, video and screenshots as
attachments, a deep link back to the capture, and structured labels derived from
`projectId` plus detected error signatures.

Phase 1 moves dispatch server-side into `router`, behind a transactional outbox
shared with webhooks — one retry-and-backoff implementation, not two. The provider
interface is unchanged; only its call site moves.

## Alternatives Considered

### User-supplied Personal Access Token

- Pros: Trivial to implement — no OAuth flow, no app registration, no refresh
  handling. Works identically across both providers.
- Cons: A long-lived broad-scope credential in extension storage on every QA machine.
  Revocation is per-user and manual. No distinction in audit logs between the tool
  and the user. Users habitually over-scope tokens because narrowing them is fiddly.
- Rejected: The wrong shape under ISO 27001 access control, and the failure mode —
  a leaked token with write access to every repository the user can reach — is severe.

### GitHub OAuth App with a shipped client secret

- Pros: Standard authorization-code flow, familiar, good UX with a redirect.
- Cons: The secret is not secret. It is extractable from the extension bundle by
  anyone who installs it, and rotating it invalidates every install.
- Rejected: A public client cannot hold a secret. This is not a risk assessment, it
  is a definition.

### A minimal auth-broker service in Phase 0 to hold secrets

- Pros: Enables the standard authorization-code flow for both providers. Small
  service.
- Cons: Contradicts ADR-002. And a service holding OAuth secrets and minting tokens
  is not a small compliance object regardless of its line count.
- Rejected: Device flow and PKCE solve the problem with no service at all.

### GitLab only, per the PRD, deferring GitHub

- Pros: One implementation for Phase 0. Matches the existing ERP stack and the AI
  code review agent's provider work.
- Cons: Contradicts the stated priority. Also leaves the provider interface
  unvalidated — a one-implementation interface reliably encodes that
  implementation's assumptions, and the rework surfaces exactly when the second
  provider is added under time pressure.
- Rejected: Two implementations up front is the cheaper path to a correct
  abstraction.

### Direct `git` operations instead of tracker APIs

- Pros: No OAuth at all if a credential helper is already configured.
- Cons: Issues are not git objects. There is no git operation that creates one.
- Rejected: Does not address the requirement.

## Consequences

- **Two dissimilar auth flows to build and test.** Device flow polls a token endpoint
  and shows the user a code to enter on github.com; PKCE performs a redirect with a
  challenge. These share almost no code below the `authorize()` boundary, which is a
  real cost — and the reason the interface is defined at `authorize()` rather than
  somewhere more granular.
- **Device flow has a distinctive UX.** The user leaves the extension, visits
  `github.com/login/device`, and types an eight-character code. More friction than a
  redirect, and it needs clear in-extension guidance. Offsetting benefit: no redirect
  URI registration, which for an extension whose ID varies between the policy-pushed
  and Web Store builds (ADR-010) is a genuine simplification.
- **Attachment handling differs by provider.** GitHub has no attachment API — files
  are uploaded through a separate authenticated endpoint and referenced by URL, and
  video attachments have size limits. GitLab has a project uploads API. The
  `attachAssets` boundary exists to contain that asymmetry. Where an asset exceeds a
  provider's limit, the issue body links to the capture instead of embedding it.
- **The interface must not leak either provider's vocabulary.** No `iid`, no
  `node_id`, no provider-shaped label semantics above the boundary. Enforced by
  writing both implementations before either ships.
- **Token refresh must survive the service worker being killed.** MV3 workers are
  evicted aggressively; refresh state lives in storage, not memory.
- Adding a third tracker (Jira, Linear) later is contained work against a validated
  interface. Moving dispatch server-side in Phase 1 does not touch the interface.
