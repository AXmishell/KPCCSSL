package store

import (
	"context"
	"database/sql"
)

// ListDNSCredentials returns credentials without decrypting them.
func (s *Store) ListDNSCredentials(ctx context.Context) ([]DNSCredential, error) {
	rows, err := s.db.QueryContext(ctx, dnsCols+` ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DNSCredential{}
	for rows.Next() {
		d, err := s.scanDNS(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// GetDNSCredential returns one credential.
func (s *Store) GetDNSCredential(ctx context.Context, id string) (*DNSCredential, error) {
	return s.scanDNS(s.db.QueryRowContext(ctx, dnsCols+` WHERE id=?`, id))
}

// CreateDNSCredential inserts a credential.
func (s *Store) CreateDNSCredential(ctx context.Context, d *DNSCredential) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO dns_credentials
		(id,name,provider,config_enc,key_version,status,last_error,last_used_at,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		d.ID, d.Name, d.Provider, d.ConfigEnc, d.KeyVersion, d.Status, d.LastError, d.LastUsedAt, d.CreatedAt, d.UpdatedAt)
	return err
}

// UpdateDNSCredential updates mutable fields.
func (s *Store) UpdateDNSCredential(ctx context.Context, d *DNSCredential) error {
	_, err := s.db.ExecContext(ctx, `UPDATE dns_credentials SET name=?,provider=?,config_enc=?,key_version=?,status=?,last_error=?,updated_at=? WHERE id=?`,
		d.Name, d.Provider, d.ConfigEnc, d.KeyVersion, d.Status, d.LastError, d.UpdatedAt, d.ID)
	return err
}

// SetDNSCredentialHealth records validation result / last use.
func (s *Store) SetDNSCredentialHealth(ctx context.Context, id, status string, lastErr *string, usedAt *int64, now int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE dns_credentials SET status=?,last_error=?,last_used_at=COALESCE(?,last_used_at),updated_at=? WHERE id=?`,
		status, lastErr, usedAt, now, id)
	return err
}

// DeleteDNSCredential deletes a credential.
func (s *Store) DeleteDNSCredential(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM dns_credentials WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// CountCertificatesUsingCredential counts non-deleted certificates that
// reference a credential; deletion is refused while > 0.
func (s *Store) CountCertificatesUsingCredential(ctx context.Context, id string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM certificates WHERE dns_credential_id=?`, id).Scan(&n)
	return n, err
}

const dnsCols = `SELECT id,name,provider,config_enc,key_version,status,last_error,last_used_at,created_at,updated_at FROM dns_credentials`

func (s *Store) scanDNS(row interface{ Scan(...any) error }) (*DNSCredential, error) {
	var d DNSCredential
	var lastErr sql.NullString
	var used sql.NullInt64
	if err := row.Scan(&d.ID, &d.Name, &d.Provider, &d.ConfigEnc, &d.KeyVersion, &d.Status, &lastErr, &used, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return nil, notFound(err)
	}
	d.LastError = nullPtr(lastErr)
	if used.Valid {
		d.LastUsedAt = &used.Int64
	}
	return &d, nil
}
