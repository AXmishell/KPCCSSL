-- +goose Up
-- Explicit opt-in for deploying a certificate to its enabled targets after a
-- successful issue/renew. Defaults to on to preserve existing behaviour.
ALTER TABLE certificates ADD COLUMN auto_deploy INTEGER NOT NULL DEFAULT 1;

-- +goose Down
-- Down migrations are intentionally unsupported in production.
