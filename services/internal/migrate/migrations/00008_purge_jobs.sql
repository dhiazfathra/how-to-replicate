-- +goose Up
-- Task 13: the durable purge state machine. One row per capture being hard
-- deleted, advancing through explicit states so a crash at any point can be
-- resumed by re-reading this row rather than guessing what already
-- happened. object_keys holds the asset object keys captured at the
-- tombstoned->blobs-deleting transition, so a resumed blobs-deleting step
-- knows exactly what remains to delete without re-deriving it from assets
-- (which the final purged step removes).
--
-- Unlike capture_events/mutations/comments/audit_log, this table is NOT
-- append-only: advancing state is an UPDATE in place, and it is mutated by
-- the same runtime role as captures/assets (full CRUD, no reject_mutation
-- trigger) — a purge_jobs row's job is exactly to change over its own
-- lifetime.
-- +goose StatementBegin
CREATE TABLE purge_jobs (
    id TEXT PRIMARY KEY,
    capture_id TEXT NOT NULL UNIQUE REFERENCES captures(id),
    workspace_id TEXT NOT NULL REFERENCES workspaces(id),
    state TEXT NOT NULL DEFAULT 'pending'
        CHECK (state IN ('pending', 'tombstoned', 'blobs-deleting', 'blobs-deleted', 'purged')),
    object_keys JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- The reconciliation sweep's primary lookup: every job not yet at the
-- terminal state, to re-drive.
-- +goose StatementBegin
CREATE INDEX purge_jobs_state_idx ON purge_jobs(state);
-- +goose StatementEnd

-- +goose StatementBegin
GRANT SELECT, INSERT, UPDATE ON purge_jobs TO htr_runtime;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS purge_jobs CASCADE;
-- +goose StatementEnd
