-- +goose Up
-- Scheduler support: queued jobs with a future available_at are not claimed yet.
ALTER TABLE jobs ADD COLUMN available_at INTEGER NOT NULL DEFAULT 0;
CREATE INDEX idx_jobs_available ON jobs(status, available_at);

-- TOTP recovery codes (single use, hashed).
CREATE TABLE totp_recovery_codes (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  code_hash BLOB NOT NULL,
  used_at INTEGER,
  created_at INTEGER NOT NULL
);
CREATE INDEX idx_recovery_user ON totp_recovery_codes(user_id);

-- +goose Down
DROP TABLE IF EXISTS totp_recovery_codes;
DROP INDEX IF EXISTS idx_jobs_available;
ALTER TABLE jobs DROP COLUMN available_at;
