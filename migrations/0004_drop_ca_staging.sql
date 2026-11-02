-- +goose Up
ALTER TABLE ca_accounts DROP COLUMN is_staging;

-- +goose Down
ALTER TABLE ca_accounts ADD COLUMN is_staging INTEGER NOT NULL DEFAULT 0;
