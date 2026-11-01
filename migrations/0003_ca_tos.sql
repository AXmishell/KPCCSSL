-- +goose Up
ALTER TABLE ca_accounts ADD COLUMN tos_accepted_at INTEGER;

-- +goose Down
ALTER TABLE ca_accounts DROP COLUMN tos_accepted_at;
