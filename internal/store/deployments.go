package store

import (
	"context"
	"database/sql"
)

// ListDeployments returns deployments, optionally scoped to a certificate.
func (s *Store) ListDeployments(ctx context.Context, certID string) ([]Deployment, error) {
	q := deployCols + ` `
	var args []any
	if certID != "" {
		q += `WHERE certificate_id=? `
		args = append(args, certID)
	}
	q += `ORDER BY created_at ASC`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Deployment{}
	for rows.Next() {
		d, err := s.scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// GetDeployment returns one deployment target.
func (s *Store) GetDeployment(ctx context.Context, id string) (*Deployment, error) {
	return s.scanDeployment(s.db.QueryRowContext(ctx, deployCols+` WHERE id=?`, id))
}

// CreateDeployment inserts a deployment target.
func (s *Store) CreateDeployment(ctx context.Context, d *Deployment) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO deployments
		(id,certificate_id,name,type,credential_id,config_enc,key_version,enabled,last_run_at,last_status,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		d.ID, d.CertificateID, d.Name, d.Type, d.CredentialID, d.ConfigEnc, d.KeyVersion, boolInt(d.Enabled), d.LastRunAt, d.LastStatus, d.CreatedAt, d.UpdatedAt)
	return err
}

// UpdateDeployment updates a target.
func (s *Store) UpdateDeployment(ctx context.Context, d *Deployment) error {
	_, err := s.db.ExecContext(ctx, `UPDATE deployments SET name=?,type=?,credential_id=?,config_enc=?,key_version=?,enabled=?,updated_at=? WHERE id=?`,
		d.Name, d.Type, d.CredentialID, d.ConfigEnc, d.KeyVersion, boolInt(d.Enabled), d.UpdatedAt, d.ID)
	return err
}

// DeleteDeployment removes a target.
func (s *Store) DeleteDeployment(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM deployments WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetDeploymentLastRun records the outcome shown in listings.
func (s *Store) SetDeploymentLastRun(ctx context.Context, id, status string, now int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE deployments SET last_run_at=?,last_status=? WHERE id=?`, now, status, id)
	return err
}

// CreateDeploymentRun appends a run history row.
func (s *Store) CreateDeploymentRun(ctx context.Context, r *DeploymentRun) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO deployment_runs (id,deployment_id,revision_id,status,message,created_at) VALUES (?,?,?,?,?,?)`,
		r.ID, r.DeploymentID, r.RevisionID, r.Status, r.Message, r.CreatedAt)
	return err
}

// FinishDeploymentRun records the outcome of a run.
func (s *Store) FinishDeploymentRun(ctx context.Context, id, status string, message *string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE deployment_runs SET status=?,message=? WHERE id=?`, status, message, id)
	return err
}

// ListDeploymentRuns returns recent runs for a deployment.
func (s *Store) ListDeploymentRuns(ctx context.Context, deploymentID string, limit int) ([]DeploymentRun, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,deployment_id,revision_id,status,message,created_at FROM deployment_runs WHERE deployment_id=? ORDER BY created_at DESC LIMIT ?`, deploymentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeploymentRun{}
	for rows.Next() {
		var r DeploymentRun
		var msg sql.NullString
		if err := rows.Scan(&r.ID, &r.DeploymentID, &r.RevisionID, &r.Status, &msg, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.Message = nullPtr(msg)
		out = append(out, r)
	}
	return out, rows.Err()
}

const deployCols = `SELECT id,certificate_id,name,type,credential_id,config_enc,key_version,enabled,last_run_at,last_status,created_at,updated_at FROM deployments`

func (s *Store) scanDeployment(row interface{ Scan(...any) error }) (*Deployment, error) {
	var d Deployment
	var enabled int
	var credentialID sql.NullString
	var lastRun sql.NullInt64
	var lastStatus sql.NullString
	if err := row.Scan(&d.ID, &d.CertificateID, &d.Name, &d.Type, &credentialID, &d.ConfigEnc, &d.KeyVersion, &enabled, &lastRun, &lastStatus, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return nil, notFound(err)
	}
	d.Enabled = enabled != 0
	d.CredentialID = nullPtr(credentialID)
	if lastRun.Valid {
		d.LastRunAt = &lastRun.Int64
	}
	d.LastStatus = nullPtr(lastStatus)
	return &d, nil
}
