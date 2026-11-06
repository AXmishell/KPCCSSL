-- +goose NO TRANSACTION

-- +goose Up
-- SQLite cannot alter a CHECK constraint in place. Rebuild the table while
-- foreign keys are off so deployment_runs rows are not cascaded away.
PRAGMA foreign_keys=OFF;
BEGIN;
CREATE TABLE deployments_new (
  id TEXT PRIMARY KEY,
  certificate_id TEXT NOT NULL REFERENCES certificates(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  type TEXT NOT NULL CHECK (type IN (
    'local_file','reload','webhook',
    'ssh',
    'baota_panel','baota_site',
    '1panel_panel','1panel_site',
    'tencent_cdn','tencent_waf',
    'aliyun_cdn','aliyun_waf'
  )),
  config_enc TEXT NOT NULL,
  key_version INTEGER NOT NULL DEFAULT 1,
  enabled INTEGER NOT NULL DEFAULT 1,
  last_run_at INTEGER,
  last_status TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
INSERT INTO deployments_new
  (id,certificate_id,name,type,config_enc,key_version,enabled,last_run_at,last_status,created_at,updated_at)
SELECT
  id,certificate_id,name,type,config_enc,key_version,enabled,last_run_at,last_status,created_at,updated_at
FROM deployments;
DROP TABLE deployments;
ALTER TABLE deployments_new RENAME TO deployments;
COMMIT;
PRAGMA foreign_keys=ON;

-- +goose Down
PRAGMA foreign_keys=OFF;
BEGIN;
CREATE TABLE deployments_old (
  id TEXT PRIMARY KEY,
  certificate_id TEXT NOT NULL REFERENCES certificates(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  type TEXT NOT NULL CHECK (type IN ('local_file','reload','webhook')),
  config_enc TEXT NOT NULL,
  key_version INTEGER NOT NULL DEFAULT 1,
  enabled INTEGER NOT NULL DEFAULT 1,
  last_run_at INTEGER,
  last_status TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
INSERT INTO deployments_old
  (id,certificate_id,name,type,config_enc,key_version,enabled,last_run_at,last_status,created_at,updated_at)
SELECT
  id,certificate_id,name,type,config_enc,key_version,enabled,last_run_at,last_status,created_at,updated_at
FROM deployments
WHERE type IN ('local_file','reload','webhook');
DROP TABLE deployments;
ALTER TABLE deployments_old RENAME TO deployments;
COMMIT;
PRAGMA foreign_keys=ON;
