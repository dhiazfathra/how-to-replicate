-- +goose Up
-- Task 6 (sync-gateway delta pull): mutations already carries every
-- change, but not in a shape PullDeltas can page through — it has no
-- workspace scope and no ordering column, only capture_id and created_at
-- (not a monotonic cursor two mutations landing in the same transaction
-- can tie on). seq is a global BIGSERIAL so "since=<seq>" is an unambiguous
-- resume point across every capture in a workspace, and workspace_id lets
-- PullDeltas filter without joining back to captures (which may itself
-- have moved workspace under a future feature — mutations' own workspace_id
-- is fixed at the time the mutation was applied, matching the append-only
-- semantics of the table it lives in).
-- +goose StatementBegin
ALTER TABLE mutations ADD COLUMN workspace_id TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE mutations ADD COLUMN seq BIGSERIAL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX mutations_workspace_seq_idx ON mutations (workspace_id, seq);
-- +goose StatementEnd

-- BIGSERIAL creates an implicit sequence that htr_runtime must be able to
-- advance (nextval) on every insert; ALTER DEFAULT PRIVILEGES from
-- 00002_schema.sql only covers tables, not sequences.
-- +goose StatementBegin
GRANT USAGE, SELECT ON SEQUENCE mutations_seq_seq TO htr_runtime;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS mutations_workspace_seq_idx;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE mutations DROP COLUMN IF EXISTS seq;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE mutations DROP COLUMN IF EXISTS workspace_id;
-- +goose StatementEnd
