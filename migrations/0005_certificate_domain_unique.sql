-- +goose Up
-- One live certificate per primary domain: acme.sh keeps state in a directory
-- named after the main domain, so duplicates would silently share/overwrite
-- that state. Triggers (instead of a unique index) keep migration safe for
-- installations that already contain duplicates: existing rows are left alone,
-- but no new duplicates can be created.
-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS trg_cert_primary_domain_unique_insert
BEFORE INSERT ON certificates
WHEN NEW.deleted_at IS NULL
  AND EXISTS (
    SELECT 1 FROM certificates
    WHERE primary_domain = NEW.primary_domain AND deleted_at IS NULL
  )
BEGIN
  SELECT RAISE(ABORT, 'certificate primary domain already exists');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS trg_cert_primary_domain_unique_update
BEFORE UPDATE OF primary_domain, deleted_at ON certificates
WHEN NEW.deleted_at IS NULL
  AND EXISTS (
    SELECT 1 FROM certificates
    WHERE primary_domain = NEW.primary_domain
      AND deleted_at IS NULL
      AND id <> NEW.id
  )
BEGIN
  SELECT RAISE(ABORT, 'certificate primary domain already exists');
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS trg_cert_primary_domain_unique_update;
DROP TRIGGER IF EXISTS trg_cert_primary_domain_unique_insert;
