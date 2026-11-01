package store

import (
	"context"
	"database/sql"
)

// ListCAAccounts returns all CA accounts ordered by creation time.
func (s *Store) ListCAAccounts(ctx context.Context) ([]CAAccount, error) {
	rows, err := s.db.QueryContext(ctx, caCols+` ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CAAccount{}
	for rows.Next() {
		a, err := s.scanCA(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// GetCAAccount returns one CA account.
func (s *Store) GetCAAccount(ctx context.Context, id string) (*CAAccount, error) {
	return s.scanCA(s.db.QueryRowContext(ctx, caCols+` WHERE id=?`, id))
}

// CreateCAAccount inserts a CA account.
func (s *Store) CreateCAAccount(ctx context.Context, a *CAAccount) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO ca_accounts
		(id,name,directory_url,is_staging,email,eab_kid,eab_hmac_enc,tos_accepted_at,key_version,status,last_error,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.Name, a.DirectoryURL, boolInt(a.IsStaging), a.Email, a.EABKID, a.EABHMACEnc, a.TOSAcceptedAt, a.KeyVersion, a.Status, a.LastError, a.CreatedAt, a.UpdatedAt)
	return err
}

// UpdateCAAccount updates mutable fields.
func (s *Store) UpdateCAAccount(ctx context.Context, a *CAAccount) error {
	_, err := s.db.ExecContext(ctx, `UPDATE ca_accounts SET name=?,directory_url=?,is_staging=?,email=?,eab_kid=?,eab_hmac_enc=?,tos_accepted_at=?,key_version=?,updated_at=? WHERE id=?`,
		a.Name, a.DirectoryURL, boolInt(a.IsStaging), a.Email, a.EABKID, a.EABHMACEnc, a.TOSAcceptedAt, a.KeyVersion, a.UpdatedAt, a.ID)
	return err
}

// SetCAStatus records registration state.
func (s *Store) SetCAStatus(ctx context.Context, id, status string, lastErr *string, now int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE ca_accounts SET status=?,last_error=?,updated_at=? WHERE id=?`, status, lastErr, now, id)
	return err
}

const caCols = `SELECT id,name,directory_url,is_staging,email,eab_kid,eab_hmac_enc,tos_accepted_at,key_version,status,last_error,created_at,updated_at FROM ca_accounts`

func (s *Store) scanCA(row interface{ Scan(...any) error }) (*CAAccount, error) {
	var a CAAccount
	var staging int
	var email, kid, hmac, lastErr sql.NullString
	var tos sql.NullInt64
	if err := row.Scan(&a.ID, &a.Name, &a.DirectoryURL, &staging, &email, &kid, &hmac, &tos, &a.KeyVersion, &a.Status, &lastErr, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, notFound(err)
	}
	a.IsStaging = staging != 0
	a.Email, a.EABKID, a.EABHMACEnc, a.LastError = nullPtr(email), nullPtr(kid), nullPtr(hmac), nullPtr(lastErr)
	if tos.Valid {
		a.TOSAcceptedAt = &tos.Int64
	}
	return &a, nil
}

func nullPtr(n sql.NullString) *string {
	if n.Valid {
		return &n.String
	}
	return nil
}
