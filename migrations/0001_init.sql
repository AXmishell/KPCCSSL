-- +goose Up
PRAGMA foreign_keys = ON;

CREATE TABLE users (
  id TEXT PRIMARY KEY,
  email TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  totp_secret_enc TEXT,
  role TEXT NOT NULL CHECK (role IN ('admin','operator','viewer')),
  disabled INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE sessions (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash BLOB NOT NULL UNIQUE,
  csrf_secret TEXT NOT NULL,
  expires_at INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL,
  ip TEXT,
  user_agent TEXT
);
CREATE INDEX idx_sessions_user ON sessions(user_id);
CREATE INDEX idx_sessions_expires ON sessions(expires_at);

CREATE TABLE ca_accounts (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  directory_url TEXT NOT NULL,
  is_staging INTEGER NOT NULL DEFAULT 0,
  email TEXT,
  eab_kid TEXT,
  eab_hmac_enc TEXT,
  key_version INTEGER NOT NULL DEFAULT 1,
  status TEXT NOT NULL CHECK (status IN ('new','registered','error','disabled')),
  last_error TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE INDEX idx_ca_directory ON ca_accounts(directory_url);

CREATE TABLE dns_credentials (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  provider TEXT NOT NULL,
  config_enc TEXT NOT NULL,
  key_version INTEGER NOT NULL DEFAULT 1,
  status TEXT NOT NULL CHECK (status IN ('unverified','ok','error')),
  last_error TEXT,
  last_used_at INTEGER,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE INDEX idx_dns_provider ON dns_credentials(provider);

CREATE TABLE certificates (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  primary_domain TEXT NOT NULL,
  san_json TEXT NOT NULL DEFAULT '[]',
  key_type TEXT NOT NULL,
  ca_account_id TEXT NOT NULL REFERENCES ca_accounts(id),
  challenge_type TEXT NOT NULL CHECK (
    challenge_type IN ('dns-01','http-01-webroot','http-01-standalone','tls-alpn-01')
  ),
  dns_credential_id TEXT REFERENCES dns_credentials(id),
  webroot TEXT,
  options_json TEXT NOT NULL DEFAULT '{}',
  auto_renew INTEGER NOT NULL DEFAULT 1,
  status TEXT NOT NULL CHECK (
    status IN ('draft','queued','issuing','active','renewing','failed','revoked','disabled')
  ),
  current_revision_id TEXT,
  next_renew_at INTEGER,
  last_error_code TEXT,
  last_error_message TEXT,
  version INTEGER NOT NULL DEFAULT 1,
  deleted_at INTEGER,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE INDEX idx_cert_status ON certificates(status, deleted_at);
CREATE INDEX idx_cert_expiry ON certificates(next_renew_at);
CREATE INDEX idx_cert_domain ON certificates(primary_domain);

CREATE TABLE certificate_revisions (
  id TEXT PRIMARY KEY,
  certificate_id TEXT NOT NULL REFERENCES certificates(id) ON DELETE CASCADE,
  serial TEXT NOT NULL,
  fingerprint_sha256 TEXT NOT NULL,
  not_before INTEGER NOT NULL,
  not_after INTEGER NOT NULL,
  cert_path TEXT NOT NULL,
  key_path TEXT NOT NULL,
  chain_path TEXT NOT NULL,
  fullchain_path TEXT NOT NULL,
  issued_at INTEGER NOT NULL,
  revoked_at INTEGER,
  revoke_reason INTEGER,
  created_at INTEGER NOT NULL
);
CREATE INDEX idx_revisions_cert ON certificate_revisions(certificate_id, issued_at DESC);
CREATE INDEX idx_revisions_expiry ON certificate_revisions(not_after);

CREATE TABLE jobs (
  id TEXT PRIMARY KEY,
  type TEXT NOT NULL CHECK (
    type IN ('register_account','issue','renew','revoke','deploy','sync')
  ),
  certificate_id TEXT REFERENCES certificates(id) ON DELETE SET NULL,
  ca_account_id TEXT REFERENCES ca_accounts(id) ON DELETE SET NULL,
  status TEXT NOT NULL CHECK (
    status IN ('queued','running','succeeded','failed','canceled','interrupted')
  ),
  attempts INTEGER NOT NULL DEFAULT 0,
  max_attempts INTEGER NOT NULL DEFAULT 3,
  payload_json TEXT NOT NULL DEFAULT '{}',
  idempotency_key TEXT,
  error_code TEXT,
  error_message TEXT,
  log_path TEXT,
  created_at INTEGER NOT NULL,
  started_at INTEGER,
  finished_at INTEGER,
  heartbeat_at INTEGER
);
CREATE UNIQUE INDEX idx_jobs_idempotency
  ON jobs(idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX idx_jobs_status ON jobs(status, created_at);
CREATE INDEX idx_jobs_cert ON jobs(certificate_id, created_at DESC);

CREATE TABLE certificate_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  certificate_id TEXT NOT NULL REFERENCES certificates(id) ON DELETE CASCADE,
  type TEXT NOT NULL,
  message TEXT,
  job_id TEXT REFERENCES jobs(id) ON DELETE SET NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX idx_cert_events ON certificate_events(certificate_id, created_at DESC);

CREATE TABLE deployments (
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

CREATE TABLE deployment_runs (
  id TEXT PRIMARY KEY,
  deployment_id TEXT NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
  revision_id TEXT NOT NULL REFERENCES certificate_revisions(id),
  status TEXT NOT NULL CHECK (status IN ('running','succeeded','failed')),
  message TEXT,
  created_at INTEGER NOT NULL
);
CREATE INDEX idx_deploy_runs ON deployment_runs(deployment_id, created_at DESC);

CREATE TABLE notification_channels (
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

CREATE TABLE notification_deliveries (
  id TEXT PRIMARY KEY,
  channel_id TEXT NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
  event_type TEXT NOT NULL,
  event_key TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('pending','sent','failed')),
  attempts INTEGER NOT NULL DEFAULT 0,
  last_error TEXT,
  created_at INTEGER NOT NULL,
  sent_at INTEGER
);
CREATE UNIQUE INDEX idx_notify_dedupe ON notification_deliveries(channel_id, event_key);

CREATE TABLE audit_logs (
  id TEXT PRIMARY KEY,
  actor_id TEXT REFERENCES users(id) ON DELETE SET NULL,
  action TEXT NOT NULL,
  resource_type TEXT NOT NULL,
  resource_id TEXT,
  ip TEXT,
  user_agent TEXT,
  request_id TEXT,
  result TEXT NOT NULL CHECK (result IN ('ok','denied','error')),
  detail_json TEXT,
  created_at INTEGER NOT NULL
);
CREATE INDEX idx_audit_created ON audit_logs(created_at DESC);
CREATE INDEX idx_audit_actor ON audit_logs(actor_id, created_at DESC);

CREATE TABLE settings (
  key TEXT PRIMARY KEY,
  value_json TEXT,
  value_enc TEXT,
  is_secret INTEGER NOT NULL DEFAULT 0,
  key_version INTEGER NOT NULL DEFAULT 1,
  updated_at INTEGER NOT NULL,
  CHECK (
    (is_secret = 0 AND value_json IS NOT NULL)
    OR (is_secret = 1 AND value_enc IS NOT NULL)
  )
);

-- +goose Down
DROP TABLE IF EXISTS settings;
DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS notification_deliveries;
DROP TABLE IF EXISTS notification_channels;
DROP TABLE IF EXISTS deployment_runs;
DROP TABLE IF EXISTS deployments;
DROP TABLE IF EXISTS certificate_events;
DROP TABLE IF EXISTS jobs;
DROP TABLE IF EXISTS certificate_revisions;
DROP TABLE IF EXISTS certificates;
DROP TABLE IF EXISTS dns_credentials;
DROP TABLE IF EXISTS ca_accounts;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS users;
