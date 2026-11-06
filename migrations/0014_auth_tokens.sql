-- +goose Up
-- Single-use bearer tokens for password reset and email-change confirmation.
-- Only the SHA-256 hash is stored; the raw token is sent to the user's email.
CREATE TABLE auth_tokens (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  purpose TEXT NOT NULL CHECK (purpose IN ('password_reset','email_change')),
  token_hash BLOB NOT NULL UNIQUE,
  email TEXT,
  expires_at INTEGER NOT NULL,
  used_at INTEGER,
  created_at INTEGER NOT NULL
);
CREATE INDEX idx_auth_tokens_user ON auth_tokens(user_id, purpose);
CREATE INDEX idx_auth_tokens_expires ON auth_tokens(expires_at);

-- +goose Down
DROP INDEX IF EXISTS idx_auth_tokens_expires;
DROP INDEX IF EXISTS idx_auth_tokens_user;
DROP TABLE IF EXISTS auth_tokens;
