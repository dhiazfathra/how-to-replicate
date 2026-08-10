-- +goose Up
-- Baseline migration. Schema lands in a later task; this establishes the
-- goose version table so the runner has something to apply from day one.
SELECT 1;

-- +goose Down
SELECT 1;
