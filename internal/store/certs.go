package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// ListCertificates filters and pages certificates. cursor is the created_at
// of the last row from the previous page (descending order).
func (s *Store) ListCertificates(ctx context.Context, status, search string, limit int, cursor int64) ([]Certificate, error) {
	q := certCols + ` WHERE deleted_at IS NULL`
	var args []any
	if status != "" {
		q += ` AND status=?`
		args = append(args, status)
	}
	if search != "" {
		q += ` AND (name LIKE ? OR primary_domain LIKE ?)`
		like := "%" + search + "%"
		args = append(args, like, like)
	}
	if cursor > 0 {
		q += ` AND created_at < ?`
		args = append(args, cursor)
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Certificate{}
	for rows.Next() {
		c, err := s.scanCert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// GetCertificate returns a non-deleted certificate.
func (s *Store) GetCertificate(ctx context.Context, id string) (*Certificate, error) {
	return s.scanCert(s.db.QueryRowContext(ctx, certCols+` WHERE id=? AND deleted_at IS NULL`, id))
}

// CreateCertificate inserts a draft certificate.
func (s *Store) CreateCertificate(ctx context.Context, c *Certificate) error {
	san, _ := json.Marshal(c.SAN)
	opts, _ := json.Marshal(c.Options)
	_, err := s.db.ExecContext(ctx, `INSERT INTO certificates
		(id,name,primary_domain,san_json,key_type,ca_account_id,challenge_type,dns_credential_id,webroot,options_json,auto_renew,status,current_revision_id,next_renew_at,last_error_code,last_error_message,version,deleted_at,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.ID, c.Name, c.PrimaryDomain, string(san), c.KeyType, c.CAAccountID, c.ChallengeType, c.DNSCredentialID, c.Webroot, string(opts), boolInt(c.AutoRenew), c.Status, c.CurrentRevisionID, c.NextRenewAt, c.LastErrorCode, c.LastErrorMessage, c.Version, c.DeletedAt, c.CreatedAt, c.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create certificate: %w", err)
	}
	return nil
}

// UpdateCertificate updates the desired configuration with optimistic
// locking. It returns ErrConflict when the version does not match.
func (s *Store) UpdateCertificate(ctx context.Context, c *Certificate) error {
	san, _ := json.Marshal(c.SAN)
	opts, _ := json.Marshal(c.Options)
	res, err := s.db.ExecContext(ctx, `UPDATE certificates SET
		name=?,primary_domain=?,san_json=?,key_type=?,ca_account_id=?,challenge_type=?,dns_credential_id=?,webroot=?,options_json=?,auto_renew=?,version=version+1,updated_at=?
		WHERE id=? AND version=? AND deleted_at IS NULL`,
		c.Name, c.PrimaryDomain, string(san), c.KeyType, c.CAAccountID, c.ChallengeType, c.DNSCredentialID, c.Webroot, string(opts), boolInt(c.AutoRenew), c.UpdatedAt, c.ID, c.Version)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrConflict
	}
	c.Version++
	return nil
}

// SetCertificateStatus updates status and error fields.
func (s *Store) SetCertificateStatus(ctx context.Context, id, status string, code, msg *string, now int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE certificates SET status=?,last_error_code=?,last_error_message=?,updated_at=?,version=version+1 WHERE id=? AND deleted_at IS NULL`, status, code, msg, now, id)
	return err
}

// MarkIssued activates a certificate and points it at a revision.
func (s *Store) MarkIssued(ctx context.Context, id, revisionID string, nextRenew int64, now int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE certificates SET status='active',current_revision_id=?,next_renew_at=?,last_error_code=NULL,last_error_message=NULL,updated_at=?,version=version+1 WHERE id=? AND deleted_at IS NULL`,
		revisionID, nextRenew, now, id)
	return err
}

// SetNextRenew updates only the renewal timestamp.
func (s *Store) SetNextRenew(ctx context.Context, id string, next, now int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE certificates SET next_renew_at=?,updated_at=? WHERE id=?`, next, now, id)
	return err
}

// MarkRevoked flips a certificate to revoked.
func (s *Store) MarkRevoked(ctx context.Context, id string, now int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE certificates SET status='revoked',auto_renew=0,updated_at=?,version=version+1 WHERE id=? AND deleted_at IS NULL`, now, id)
	return err
}

// SoftDeleteCertificate stops renewals but keeps files and audit history.
func (s *Store) SoftDeleteCertificate(ctx context.Context, id string, now int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE certificates SET deleted_at=?,auto_renew=0,updated_at=?,version=version+1 WHERE id=? AND deleted_at IS NULL`, now, now, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DueForRenewal returns active certificates whose renewal window is due.
func (s *Store) DueForRenewal(ctx context.Context, before int64, limit int) ([]Certificate, error) {
	rows, err := s.db.QueryContext(ctx, certCols+` WHERE deleted_at IS NULL AND status='active' AND auto_renew=1 AND next_renew_at IS NOT NULL AND next_renew_at <= ? ORDER BY next_renew_at ASC LIMIT ?`, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Certificate{}
	for rows.Next() {
		c, err := s.scanCert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// CertificateStats returns dashboard counters.
func (s *Store) CertificateStats(ctx context.Context) (total, expiring, failed, active int, err error) {
	now := Now()
	row := s.db.QueryRowContext(ctx, `SELECT
		COUNT(*),
		COALESCE(SUM(CASE WHEN status='active' AND next_renew_at IS NOT NULL AND next_renew_at <= ? THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN status='failed' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN status='active' THEN 1 ELSE 0 END),0)
		FROM certificates WHERE deleted_at IS NULL`, now+30*86400)
	err = row.Scan(&total, &expiring, &failed, &active)
	return
}

const certCols = `SELECT id,name,primary_domain,san_json,key_type,ca_account_id,challenge_type,dns_credential_id,webroot,options_json,auto_renew,status,current_revision_id,next_renew_at,last_error_code,last_error_message,version,deleted_at,created_at,updated_at FROM certificates`

func (s *Store) scanCert(row interface{ Scan(...any) error }) (*Certificate, error) {
	var c Certificate
	var sanJSON, optsJSON, status string
	var cred, webroot, current, code, msg sql.NullString
	var nextRenew, deleted sql.NullInt64
	var renew int
	if err := row.Scan(&c.ID, &c.Name, &c.PrimaryDomain, &sanJSON, &c.KeyType, &c.CAAccountID, &c.ChallengeType, &cred, &webroot, &optsJSON, &renew, &c.Status, &current, &nextRenew, &code, &msg, &c.Version, &deleted, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, notFound(err)
	}
	_ = status
	_ = json.Unmarshal([]byte(sanJSON), &c.SAN)
	if c.SAN == nil {
		c.SAN = []string{}
	}
	_ = json.Unmarshal([]byte(optsJSON), &c.Options)
	c.AutoRenew = renew != 0
	c.DNSCredentialID = nullPtr(cred)
	c.Webroot = nullPtr(webroot)
	c.CurrentRevisionID = nullPtr(current)
	c.LastErrorCode = nullPtr(code)
	c.LastErrorMessage = nullPtr(msg)
	if nextRenew.Valid {
		c.NextRenewAt = &nextRenew.Int64
	}
	if deleted.Valid {
		c.DeletedAt = &deleted.Int64
	}
	return &c, nil
}

// CreateRevision inserts an issued certificate revision.
func (s *Store) CreateRevision(ctx context.Context, r *Revision) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO certificate_revisions
		(id,certificate_id,serial,fingerprint_sha256,not_before,not_after,cert_path,key_path,chain_path,fullchain_path,issued_at,revoked_at,revoke_reason,created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.CertificateID, r.Serial, r.FingerprintSHA256, r.NotBefore, r.NotAfter, r.CertPath, r.KeyPath, r.ChainPath, r.FullchainPath, r.IssuedAt, r.RevokedAt, r.RevokeReason, r.CreatedAt)
	return err
}

// GetRevision returns one revision.
func (s *Store) GetRevision(ctx context.Context, id string) (*Revision, error) {
	var r Revision
	var revoked, reason sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT id,certificate_id,serial,fingerprint_sha256,not_before,not_after,cert_path,key_path,chain_path,fullchain_path,issued_at,revoked_at,revoke_reason,created_at FROM certificate_revisions WHERE id=?`, id).Scan(
		&r.ID, &r.CertificateID, &r.Serial, &r.FingerprintSHA256, &r.NotBefore, &r.NotAfter, &r.CertPath, &r.KeyPath, &r.ChainPath, &r.FullchainPath, &r.IssuedAt, &revoked, &reason, &r.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	if revoked.Valid {
		r.RevokedAt = &revoked.Int64
	}
	if reason.Valid {
		r.RevokeReason = &reason.Int64
	}
	return &r, nil
}

// ListRevisions returns revisions newest first.
func (s *Store) ListRevisions(ctx context.Context, certID string) ([]Revision, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,certificate_id,serial,fingerprint_sha256,not_before,not_after,cert_path,key_path,chain_path,fullchain_path,issued_at,revoked_at,revoke_reason,created_at FROM certificate_revisions WHERE certificate_id=? ORDER BY issued_at DESC`, certID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Revision{}
	for rows.Next() {
		var r Revision
		var revoked, reason sql.NullInt64
		if err := rows.Scan(&r.ID, &r.CertificateID, &r.Serial, &r.FingerprintSHA256, &r.NotBefore, &r.NotAfter, &r.CertPath, &r.KeyPath, &r.ChainPath, &r.FullchainPath, &r.IssuedAt, &revoked, &reason, &r.CreatedAt); err != nil {
			return nil, err
		}
		if revoked.Valid {
			r.RevokedAt = &revoked.Int64
		}
		if reason.Valid {
			r.RevokeReason = &reason.Int64
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkRevisionRevoked records revocation metadata.
func (s *Store) MarkRevisionRevoked(ctx context.Context, id string, reason int64, now int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE certificate_revisions SET revoked_at=?,revoke_reason=? WHERE id=?`, now, reason, id)
	return err
}

// AddEvent appends a certificate timeline entry.
func (s *Store) AddEvent(ctx context.Context, e *Event) error {
	res, err := s.db.ExecContext(ctx, `INSERT INTO certificate_events (certificate_id,type,message,job_id,created_at) VALUES (?,?,?,?,?)`,
		e.CertificateID, e.Type, e.Message, e.JobID, e.CreatedAt)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	e.ID = id
	return nil
}

// ListEvents returns timeline entries newest first.
func (s *Store) ListEvents(ctx context.Context, certID string, limit int) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,certificate_id,type,message,job_id,created_at FROM certificate_events WHERE certificate_id=? ORDER BY created_at DESC, id DESC LIMIT ?`, certID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var msg, job sql.NullString
		if err := rows.Scan(&e.ID, &e.CertificateID, &e.Type, &msg, &job, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Message, e.JobID = nullPtr(msg), nullPtr(job)
		out = append(out, e)
	}
	return out, rows.Err()
}

// SearchCertificatesByDomain is used by the startup reconciliation.
func (s *Store) SearchCertificatesByDomain(ctx context.Context, domain string) (*Certificate, error) {
	return s.scanCert(s.db.QueryRowContext(ctx, certCols+` WHERE primary_domain=? AND deleted_at IS NULL LIMIT 1`, strings.ToLower(domain)))
}
