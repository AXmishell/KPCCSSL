-- +goose NO TRANSACTION

-- +goose Up
-- Extend deployment targets with Tencent COS/EdgeOne, Alibaba Cloud OSS/DCDN
-- and Qiniu CDN, and notification channels with Feishu, DingTalk and WeCom.
-- SQLite cannot alter a CHECK constraint in place, so rebuild affected tables
-- while foreign keys are off to preserve dependent rows.
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
    'tencent_cdn','tencent_waf','tencent_cos','tencent_teo',
    'aliyun_cdn','aliyun_waf','aliyun_oss','aliyun_dcdn',
    'qiniu_cdn'
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

CREATE TABLE notification_channels_new (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  type TEXT NOT NULL CHECK (type IN ('smtp','webhook','telegram','feishu','dingtalk','wecom')),
  config_enc TEXT NOT NULL,
  key_version INTEGER NOT NULL DEFAULT 1,
  events_json TEXT NOT NULL DEFAULT '[]',
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
INSERT INTO notification_channels_new
  (id,name,type,config_enc,key_version,events_json,enabled,created_at,updated_at)
SELECT
  id,name,type,config_enc,key_version,events_json,enabled,created_at,updated_at
FROM notification_channels;
DROP TABLE notification_channels;
ALTER TABLE notification_channels_new RENAME TO notification_channels;

COMMIT;
PRAGMA foreign_keys=ON;

-- +goose Down
PRAGMA foreign_keys=OFF;
BEGIN;

CREATE TABLE deployments_old (
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
INSERT INTO deployments_old
  (id,certificate_id,name,type,config_enc,key_version,enabled,last_run_at,last_status,created_at,updated_at)
SELECT
  id,certificate_id,name,type,config_enc,key_version,enabled,last_run_at,last_status,created_at,updated_at
FROM deployments
WHERE type IN (
  'local_file','reload','webhook',
  'ssh',
  'baota_panel','baota_site',
  '1panel_panel','1panel_site',
  'tencent_cdn','tencent_waf',
  'aliyun_cdn','aliyun_waf'
);
DROP TABLE deployments;
ALTER TABLE deployments_old RENAME TO deployments;

CREATE TABLE notification_channels_old (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  type TEXT NOT NULL CHECK (type IN ('smtp','webhook','telegram')),
  config_enc TEXT NOT NULL,
  key_version INTEGER NOT NULL DEFAULT 1,
  events_json TEXT NOT NULL DEFAULT '[]',
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
INSERT INTO notification_channels_old
  (id,name,type,config_enc,key_version,events_json,enabled,created_at,updated_at)
SELECT
  id,name,type,config_enc,key_version,events_json,enabled,created_at,updated_at
FROM notification_channels
WHERE type IN ('smtp','webhook','telegram');
DROP TABLE notification_channels;
ALTER TABLE notification_channels_old RENAME TO notification_channels;

COMMIT;
PRAGMA foreign_keys=ON;
