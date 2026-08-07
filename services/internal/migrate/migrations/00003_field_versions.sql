-- +goose Up
-- +goose StatementBegin
-- capture_field_versions is the last-write-wins bookkeeping table for
-- sync-gateway (Task 4 / ADR-012): one row per (capture, mutable field)
-- holding the tuple — server-received timestamp, revision, mutation id —
-- of whichever mutation currently owns that field. It is mutated in place
-- (upserted) on every winning write, unlike the append-only tables above,
-- so it gets full CRUD for htr_runtime rather than SELECT+INSERT only.
CREATE TABLE capture_field_versions (
    capture_id TEXT NOT NULL REFERENCES captures(id),
    field TEXT NOT NULL,
    server_t TIMESTAMPTZ NOT NULL,
    revision BIGINT NOT NULL,
    mutation_id TEXT NOT NULL,
    PRIMARY KEY (capture_id, field)
);
-- +goose StatementEnd

-- +goose StatementBegin
GRANT SELECT, INSERT, UPDATE, DELETE ON capture_field_versions TO htr_runtime;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS capture_field_versions;
-- +goose StatementEnd
