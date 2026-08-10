-- +goose Up
-- Tracks every presigned-PUT issuance for an asset upload, independent of
-- the assets row itself, so single-use can be enforced even though a
-- server-selected object key alone is not write-once (a presigned URL stays
-- usable until it expires — see Task 5 brief / ADR-011). Each row is one
-- issuance; consumed_at is set exactly once, at the CompleteAssetUpload call
-- that first verifies it, so any replay after that finds consumed_at
-- already set and is refused without re-verifying.
-- +goose StatementBegin
CREATE TABLE asset_upload_presigns (
    id TEXT PRIMARY KEY,
    asset_id TEXT NOT NULL REFERENCES assets(id),
    object_key TEXT NOT NULL UNIQUE,
    checksum_sha256 TEXT NOT NULL,
    size_bytes BIGINT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX asset_upload_presigns_asset_id_idx ON asset_upload_presigns (asset_id);
-- +goose StatementEnd

-- +goose StatementBegin
GRANT SELECT, INSERT, UPDATE ON asset_upload_presigns TO htr_runtime;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS asset_upload_presigns;
-- +goose StatementEnd
