-- +goose Up
-- Reusable authentication credentials for deployment targets. Secrets live
-- here instead of being duplicated into every target's config.
CREATE TABLE deployment_credentials (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  type TEXT NOT NULL CHECK (type IN ('tencent','aliyun','qiniu','baota','1panel','ssh')),
  config_enc TEXT NOT NULL,
  key_version INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

ALTER TABLE deployments ADD COLUMN credential_id TEXT
  REFERENCES deployment_credentials(id) ON DELETE RESTRICT;
CREATE INDEX idx_deployments_credential ON deployments(credential_id);

-- +goose Down
DROP INDEX IF EXISTS idx_deployments_credential;
ALTER TABLE deployments DROP COLUMN credential_id;
DROP TABLE IF EXISTS deployment_credentials;
