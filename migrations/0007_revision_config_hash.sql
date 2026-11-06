-- +goose Up
-- Hash of the desired configuration at issuance time. Renewal compares it so
-- changed CA/challenge/provider/webroot settings trigger a reissue instead of
-- silently reusing acme.sh's old domain configuration.
ALTER TABLE certificate_revisions ADD COLUMN config_hash TEXT;

-- +goose Down
ALTER TABLE certificate_revisions DROP COLUMN config_hash;
