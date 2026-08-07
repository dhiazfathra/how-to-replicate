-- name: CreateWorkspace :one
INSERT INTO workspaces (id, name) VALUES ($1, $2) RETURNING *;

-- name: GetWorkspace :one
SELECT * FROM workspaces WHERE id = $1;

-- name: UpdateWorkspace :one
UPDATE workspaces SET name = $2 WHERE id = $1 RETURNING *;

-- name: DeleteWorkspace :exec
DELETE FROM workspaces WHERE id = $1;

-- name: ListWorkspacesForUser :many
SELECT w.* FROM workspaces w
JOIN memberships m ON m.workspace_id = w.id
WHERE m.user_id = $1
ORDER BY w.created_at;

-- Same no-existence-leak shape as GetProjectForWorkspace/GetAssetForWorkspace:
-- a workspace id the caller isn't a member of returns pgx.ErrNoRows
-- identically to a workspace id that doesn't exist at all.
-- name: GetWorkspaceForMember :one
SELECT w.* FROM workspaces w
JOIN memberships m ON m.workspace_id = w.id
WHERE w.id = $1 AND m.user_id = $2;

-- name: UpdateWorkspacePolicyOverrides :one
UPDATE workspaces SET policy_overrides = $2 WHERE id = $1 RETURNING *;

-- name: CreateProject :one
INSERT INTO projects (id, workspace_id, name) VALUES ($1, $2, $3) RETURNING *;

-- name: GetProject :one
SELECT * FROM projects WHERE id = $1;

-- Scoped by workspace_id so a project ID belonging to another workspace
-- returns pgx.ErrNoRows rather than another workspace's data — the
-- non-disclosing cross-workspace read.
-- name: GetProjectForWorkspace :one
SELECT * FROM projects WHERE id = $1 AND workspace_id = $2;

-- name: ListProjectsByWorkspace :many
SELECT * FROM projects WHERE workspace_id = $1 ORDER BY created_at;

-- name: UpdateProjectForWorkspace :one
UPDATE projects SET name = $3 WHERE id = $1 AND workspace_id = $2 RETURNING *;

-- name: DeleteProjectForWorkspace :execrows
DELETE FROM projects WHERE id = $1 AND workspace_id = $2;

-- name: CreateUser :one
INSERT INTO users (id, email, name) VALUES ($1, $2, $3) RETURNING *;

-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- Identity is minted by the IdP (the OIDC subject), not by us, so first
-- login upserts a local user row keyed on that subject rather than
-- inserting and failing on conflict.
-- name: UpsertUser :one
INSERT INTO users (id, email, name) VALUES ($1, $2, $3)
ON CONFLICT (id) DO UPDATE SET email = EXCLUDED.email, name = EXCLUDED.name
RETURNING *;

-- name: CreateMembership :one
INSERT INTO memberships (id, workspace_id, user_id, role) VALUES ($1, $2, $3, $4) RETURNING *;

-- name: GetMembership :one
SELECT * FROM memberships WHERE id = $1;

-- The RBAC seam: resolves a caller's role in a workspace without
-- distinguishing "no such workspace" from "not a member" — both are zero
-- rows, and callers must turn that into the same not-found response either
-- way (see internal/authz and sync-gateway/internal/authctx.OIDCMiddleware).
-- name: GetMembershipByWorkspaceAndUser :one
SELECT * FROM memberships WHERE workspace_id = $1 AND user_id = $2;

-- name: CreateRedactionRuleset :one
INSERT INTO redaction_rulesets (version, workspace_id, rules) VALUES ($1, $2, $3) RETURNING *;

-- name: GetRedactionRuleset :one
SELECT * FROM redaction_rulesets WHERE version = $1;

-- name: CreateCapture :one
INSERT INTO captures (
    id, workspace_id, project_id, source, state, fidelity, epoch, env,
    metadata, doc, withheld_event_count, revision, manifest_complete,
    applied_ruleset_version
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
) RETURNING *;

-- name: GetCapture :one
SELECT * FROM captures WHERE id = $1;

-- name: CreateCaptureEvent :one
INSERT INTO capture_events (id, capture_id, t, kind, payload, redaction)
VALUES ($1, $2, $3, $4, $5, $6) RETURNING *;

-- name: GetCaptureEvent :one
SELECT * FROM capture_events WHERE id = $1;

-- name: ListCaptureEventsByCapture :many
SELECT * FROM capture_events WHERE capture_id = $1 ORDER BY t ASC;

-- name: CreateAsset :one
INSERT INTO assets (id, capture_id, kind, mime_type, size_bytes, chunk_count, sha256, object_key)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING *;

-- name: GetAsset :one
SELECT * FROM assets WHERE id = $1;

-- name: GetAssetForWorkspace :one
-- Same no-existence-leak shape as LockCaptureForWorkspace: an asset id that
-- belongs to a capture outside the caller's workspace, or doesn't exist at
-- all, is indistinguishable pgx.ErrNoRows to the caller.
SELECT a.* FROM assets a
JOIN captures c ON c.id = a.capture_id
WHERE a.id = $1 AND a.capture_id = $2 AND c.workspace_id = $3;

-- name: UpsertAssetForUpload :one
-- Creates the manifest entry (ADR-012: "the manifest entry for each asset
-- carries the expected size and content hash before the PUT is issued") on
-- first request, or repoints object_key at a freshly minted key on
-- re-request. sha256/size_bytes/verified_at are deliberately left untouched
-- here — they only ever change via MarkAssetVerified, so a re-request can
-- never itself flip a verified asset back to unverified.
INSERT INTO assets (id, capture_id, kind, mime_type, size_bytes, chunk_count, sha256, object_key)
VALUES ($1, $2, $3, $4, $5, 0, NULL, $6)
ON CONFLICT (id) DO UPDATE SET object_key = EXCLUDED.object_key
RETURNING *;

-- name: CreateAssetUploadPresign :one
INSERT INTO asset_upload_presigns (id, asset_id, object_key, checksum_sha256, size_bytes, expires_at)
VALUES ($1, $2, $3, $4, $5, $6) RETURNING *;

-- name: GetAssetUploadPresignByKey :one
SELECT * FROM asset_upload_presigns WHERE object_key = $1;

-- name: ConsumeAssetUploadPresign :one
-- Flips consumed_at exactly once. ON conflict with an already-consumed row
-- the WHERE clause excludes it, so sqlc's :one returns pgx.ErrNoRows —
-- callers read that as "already consumed, refuse" without a second query.
UPDATE asset_upload_presigns SET consumed_at = now()
WHERE object_key = $1 AND consumed_at IS NULL
RETURNING *;

-- name: MarkAssetVerified :one
UPDATE assets SET sha256 = $2, size_bytes = $3, verified_at = now() WHERE id = $1 RETURNING *;

-- name: CountAssetsForCapture :one
SELECT count(*) FROM assets WHERE capture_id = $1;

-- name: CountUnverifiedAssetsForCapture :one
SELECT count(*) FROM assets WHERE capture_id = $1 AND verified_at IS NULL;

-- name: SetCaptureManifestComplete :one
UPDATE captures SET manifest_complete = $2 WHERE id = $1 RETURNING *;

-- name: CreateMutation :one
-- Plain insert on the client-minted mutation ULID primary key. Replay is
-- idempotent because a duplicate ID hits the PK constraint; callers treat
-- that unique-violation as "already applied" rather than an error. An
-- ON CONFLICT DO UPDATE is deliberately not used here: mutations is
-- append-only (no UPDATE grant for the runtime role), so upserting would
-- defeat the same immutability this table exists to enforce.
INSERT INTO mutations (id, capture_id, op, payload, client_t)
VALUES ($1, $2, $3, $4, $5) RETURNING *;

-- name: InsertMutationIfNew :one
-- Idempotent variant of CreateMutation used by sync-gateway: ON CONFLICT DO
-- NOTHING never updates the row (mutations stays append-only, same
-- reasoning as CreateMutation above), it just makes a duplicate insert a
-- no-op instead of a unique-violation error. sqlc's :one returns
-- pgx.ErrNoRows when the conflict fires with nothing to return, which
-- callers read as "already applied, do not reprocess". workspace_id is
-- stamped here (Task 6) so PullDeltas can filter the delta log without a
-- join back to captures.
INSERT INTO mutations (id, capture_id, workspace_id, op, payload, client_t)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (id) DO NOTHING
RETURNING *;

-- name: GetMutation :one
SELECT * FROM mutations WHERE id = $1;

-- name: ListMutationsSinceRevision :many
-- Task 6's delta log: every mutation applied in workspace_id with seq >
-- since, ordered by seq so a client resumes exactly where it left off
-- regardless of which capture each mutation touched. limit is passed as
-- requested-page-size+1 by the caller so it can detect has_more without a
-- second COUNT query.
SELECT * FROM mutations
WHERE workspace_id = $1 AND seq > $2
ORDER BY seq ASC
LIMIT $3;

-- name: LockCaptureForWorkspace :one
-- Workspace and existence are checked in one predicate so a mutation
-- naming a capture outside the caller's workspace fails the same way as a
-- mutation naming a capture that doesn't exist at all — the caller cannot
-- tell the two cases apart, which is the point (no existence leak across
-- workspaces). FOR UPDATE serializes concurrent revision increments on the
-- same capture.
SELECT * FROM captures WHERE id = $1 AND workspace_id = $2 FOR UPDATE;

-- name: UpdateCaptureRevisionAndDoc :one
UPDATE captures SET revision = $2, doc = $3 WHERE id = $1 RETURNING *;

-- name: GetCaptureFieldVersion :one
SELECT * FROM capture_field_versions WHERE capture_id = $1 AND field = $2;

-- name: UpsertCaptureFieldVersion :one
INSERT INTO capture_field_versions (capture_id, field, server_t, revision, mutation_id)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (capture_id, field) DO UPDATE
    SET server_t = EXCLUDED.server_t, revision = EXCLUDED.revision, mutation_id = EXCLUDED.mutation_id
RETURNING *;

-- name: CreateComment :one
INSERT INTO comments (id, capture_id, author_id, body) VALUES ($1, $2, $3, $4) RETURNING *;

-- name: GetComment :one
SELECT * FROM comments WHERE id = $1;

-- name: CreateAuditLog :one
INSERT INTO audit_log (id, workspace_id, actor_id, action, subject, details)
VALUES ($1, $2, $3, $4, $5, $6) RETURNING *;

-- name: GetAuditLog :one
SELECT * FROM audit_log WHERE id = $1;

-- name: CreateShareLink :one
INSERT INTO share_links (id, capture_id, token, created_by, expires_at)
VALUES ($1, $2, $3, $4, $5) RETURNING *;

-- name: GetShareLink :one
SELECT * FROM share_links WHERE id = $1;

-- name: RevokeShareLink :one
UPDATE share_links SET revoked_at = now() WHERE id = $1 RETURNING *;

-- name: CreateIntegrationBinding :one
INSERT INTO integration_bindings (id, workspace_id, provider, config)
VALUES ($1, $2, $3, $4) RETURNING *;

-- name: GetIntegrationBinding :one
SELECT * FROM integration_bindings WHERE id = $1;

-- name: CreateOutboxEntry :one
INSERT INTO outbox (id, topic, payload) VALUES ($1, $2, $3) RETURNING *;

-- name: GetOutboxEntry :one
SELECT * FROM outbox WHERE id = $1;
