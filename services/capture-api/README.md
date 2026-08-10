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

## Retention windows (Task 13)

Every workspace must have a retention window chosen at creation:
`POST /v1/workspaces` requires a `retentionDays` field (positive integer)
in the body. There is no code-level default (`internal/policy.Defaults`
deliberately has none) — how long PHI-adjacent capture data is kept is a
Security/Compliance judgment, not an engineering one (spec §22 open
question 4), so a request omitting it is rejected with 400 rather than
silently falling back to a number nobody chose. `policy.Policy.Validate()`
is the single place this is enforced; `createWorkspace` stores the chosen
value as a `policy_overrides` entry in the same transaction as the
workspace and its owner membership (`store.CreateWorkspaceWithOwner`).

## The purge state machine (Task 13)

Hard-deleting a capture past its retention window means removing both its
object-storage blobs and its Postgres metadata — but object storage can't
enlist in a Postgres transaction, so there's no single atomic unit that
covers both. Instead, `internal/purge` drives one `purge_jobs` row per
capture through four explicit, resumable states:

1. **`pending`** — the capture has passed its resolved retention window.
2. **`tombstoned`** — the capture flips to `state = 'expired'` in one
   Postgres transaction (with the job row and an audit entry). This is
   where the *compliance* deadline is met: the same ready-gate every read
   path already uses (`gateCapture` et al) makes the capture immediately
   unreadable, unexportable, and unshareable — even though its blobs still
   exist. Reclaiming them afterward is an operational concern, not a
   retention breach.
3. **`blobs-deleting`** — the capture's asset object keys are frozen into
   the job row, then each is deleted idempotently
   (`storage.Client.Delete`: deleting an already-absent key is success,
   not an error) — a resumed job re-issuing deletes for keys an earlier,
   crashed attempt already removed just succeeds again.
4. **`blobs-deleted` → `purged`** — asset rows are removed and the
   capture's disclosable content columns (`doc`/`metadata`/`env`) are
   cleared, in one final transaction with the job row and an audit entry.
   The `captures` row itself is never deleted outright: `capture_events`/
   `comments`/`mutations` reference it by FK and are append-only by
   trigger (Task 3), so nothing can ever clear the way for a cascading
   delete — that immutability is deliberate, not an oversight this task
   works around.

`Pipeline.Advance` performs exactly one step and is safe to call on a job
in any state, including a fresh `pending` job or one resumed after a crash
mid-transition — `RunToCompletion` just loops it. `Pipeline.Sweep` creates
jobs for newly-expired captures (idempotent per capture via a UNIQUE
constraint); `Pipeline.Reconcile` re-drives every job not yet `purged`;
`Pipeline.DetectOrphans` lists every bucket object accounted for by
neither a live asset row nor any purge job's recorded keys — the check
that makes "no orphans" verifiable rather than assumed. `cmd/purge-job` is
the background poll loop that runs Sweep → Reconcile → DetectOrphans on an
interval (`HTR_PURGE_JOB_INTERVAL`, default 1h), reusing the same
`HTR_S3_*` object-storage environment variables as sync-gateway.

## Audit log (Task 13)

`audit_log` now covers capture creation (`store.CreateCaptureAndAudit`),
capture access (`store.GetCaptureAndAudit`, used by `getCapture`),
share-link resolution (Task 11, unchanged), and deletion
(`capture.delete.tombstoned` / `capture.delete.purged`, written by the
purge state machine's two transactional steps). Every one of these commits
the audit row in the same Postgres transaction as the action it records —
an action can never succeed unaudited. MCP reads are not implemented yet
(Phase 2), but `audit_log.action` is a plain `TEXT` column, so no schema
change is needed to add that action name later. Immutability
(`UPDATE`/`DELETE` rejected outright) is enforced by Task 3's
`reject_mutation` trigger, re-confirmed against these new write paths in
`store`'s integration tests.
