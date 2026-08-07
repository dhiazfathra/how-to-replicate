# capture-api

Workspace/project CRUD, workspace policy, capture reads, comments, and
share-link resolution. Go, chi, ConnectRPC-free plain JSON over HTTP
(matches the rest of this service — see `internal/api/router.go`), OIDC
authentication (`services/internal/auth`), workspace-scoped RBAC
(`services/internal/authz`).

## Routes

All routes below `/v1/workspaces` require a verified OIDC subject and (below
workspace creation) a real membership row, resolved fresh per request.

- `POST/GET /v1/workspaces`, `GET/PATCH/DELETE /v1/workspaces/{workspaceID}`
- `GET/PUT /v1/workspaces/{workspaceID}/policy`
- `POST/GET/PATCH/DELETE .../projects[/{projectID}]`
- `GET /v1/workspaces/{workspaceID}/captures` — list captures
- `GET /v1/workspaces/{workspaceID}/captures/{captureID}` — get a capture
- `GET /v1/workspaces/{workspaceID}/captures/{captureID}/events` — list a
  capture's events
- `POST /v1/workspaces/{workspaceID}/captures/{captureID}/comments` —
  append a comment
- `POST /v1/workspaces/{workspaceID}/captures/{captureID}/share-links` —
  mint a share link
- `POST .../share-links/{shareLinkID}/revoke` — revoke a share link

`GET /v1/share/{token}` is the one **unauthenticated** route: no-login
viewing via a share link. It is mounted outside `auth.Middleware` (see
`api.NewShareRouter`) and rate-limited per client IP.

## The ready gate (invariant 1)

CLAUDE.md's invariant 1: "no unredacted capture is viewable, exportable, or
routable. The gate is `state === 'ready'`." Every read path that can expose
a capture — `getCapture`, `listCaptures`, `listEvents`, and the share-link
resolver — routes through one function, `gateCapture` (plus
`gateCaptureEvents` for the event list), in
`internal/api/captureview.go`. For any capture not in state `ready` it
returns status metadata only: `id`, `state`, `fidelity`, `createdAt` — no
`doc`, `metadata`, `env`, `withheldEventCount`, or `events`. Those fields
are `omitempty` on the wire, so a non-ready capture's JSON has no trace of
them at all, not just empty values.

A future read path that forgets to call `gateCapture` is the only way to
break this — there's exactly one gate to remember.

## Share links

- **Format**: `base64url(payload-json) + "." + base64url(HMAC-SHA256(key,
  payload-json))`. Payload is `{keyID, shareLinkID, captureID,
  expiresUnix}` (`internal/sharelink/sharelink.go`).
- **Revocation handle**: the payload carries the share link's own random
  ID (`shareLinkID`), not the bare capture ID — so knowing/guessing a
  capture ID isn't enough to forge a plausible token, and two links
  pointing at the same capture are independently revocable.
- **Key rotation**: `sharelink.Signer` holds a `Current` key (signs new
  tokens) plus `Previous` keys (verification only, matched by the `kid` in
  the token). Configured via `HTR_SHARE_LINK_KEY_ID` / `HTR_SHARE_LINK_KEY`
  (current) and `HTR_SHARE_LINK_PREVIOUS_KEYS` (comma-separated
  `id:secret` pairs) — rotate by adding a new current key and moving the
  old one to the previous list, then drop it once its longest-lived
  outstanding tokens have expired.
- **Revocation**: `revoked_at` on the `share_links` row. Checked on every
  resolution alongside expiry and `state === 'ready'`, in the resolver
  (`internal/api/share.go`), never left to the caller.
- **Audit**: every successful resolution writes exactly one `audit_log`
  row (`action: "share_link.resolve"`).
- **Rate limiting**: an in-process, per-client-IP fixed-window limiter
  (`internal/sharelink/ratelimit.go`, default 20 requests/minute) — no new
  external dependency, consistent with this repo's single-service-per-
  deployment posture (ADR-002/ADR-012).

## Environment

| Variable | Purpose |
|---|---|
| `HTR_POSTGRES_DSN`, `HTR_RUNTIME_PASSWORD` | database connection |
| `HTR_OIDC_ISSUER`, `HTR_OIDC_CLIENT_ID` | OIDC verifier config |
| `HTR_SHARE_LINK_KEY_ID`, `HTR_SHARE_LINK_KEY` | current share-link signing key |
| `HTR_SHARE_LINK_PREVIOUS_KEYS` | optional, comma-separated `id:secret` pairs still accepted for verification |
| `HTR_LISTEN_ADDR` | defaults to `:8082` |
