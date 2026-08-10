-- +goose Up
-- Task 10: per-workspace policy overrides (storage cap, capture cap, LLM
-- provider chain, local-only, origin allow-list, retention days). Stored as
-- a partial JSON object layered over hardcoded defaults in Go
-- (capture-api's internal/policy.Resolve) rather than as individual
-- columns, so adding a new policy field never needs another migration.
-- NULL means "no overrides, use defaults" — distinct from an empty object,
-- though both resolve identically today.
-- +goose StatementBegin
ALTER TABLE workspaces ADD COLUMN policy_overrides JSONB;
-- +goose StatementEnd

-- No separate GRANT needed: workspaces already has SELECT/INSERT/UPDATE for
-- htr_runtime from 00002_schema.sql's table-level grant, which covers new
-- columns on the same table (Postgres does not grant per-column).

-- +goose Down
-- +goose StatementBegin
ALTER TABLE workspaces DROP COLUMN IF EXISTS policy_overrides;
-- +goose StatementEnd
