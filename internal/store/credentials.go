package store

import (
	"context"
)

// ListDeploymentCredentials returns reusable deployment credentials.
func (s *Store) ListDeploymentCredentials(ctx context.Context) ([]DeploymentCredential, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,type,config_enc,key_version,created_at,updated_at FROM deployment_credentials ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeploymentCredential{}
	for rows.Next() {
		var c DeploymentCredential
		if err := rows.Scan(&c.ID, &c.Name, &c.Type, &c.ConfigEnc, &c.KeyVersion, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetDeploymentCredential returns one credential by ID.
func (s *Store) GetDeploymentCredential(ctx context.Context, id string) (*DeploymentCredential, error) {
	var c DeploymentCredential
	err := s.db.QueryRowContext(ctx, `SELECT id,name,type,config_enc,key_version,created_at,updated_at FROM deployment_credentials WHERE id=?`, id).
		Scan(&c.ID, &c.Name, &c.Type, &c.ConfigEnc, &c.KeyVersion, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return &c, nil
}

// CreateDeploymentCredential inserts a credential.
func (s *Store) CreateDeploymentCredential(ctx context.Context, c *DeploymentCredential) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO deployment_credentials
		(id,name,type,config_enc,key_version,created_at,updated_at) VALUES (?,?,?,?,?,?,?)`,
		c.ID, c.Name, c.Type, c.ConfigEnc, c.KeyVersion, c.CreatedAt, c.UpdatedAt)
	return err
}

// UpdateDeploymentCredential updates a credential.
func (s *Store) UpdateDeploymentCredential(ctx context.Context, c *DeploymentCredential) error {
	res, err := s.db.ExecContext(ctx, `UPDATE deployment_credentials SET name=?,config_enc=?,key_version=?,updated_at=? WHERE id=?`,
		c.Name, c.ConfigEnc, c.KeyVersion, c.UpdatedAt, c.ID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteDeploymentCredential removes a credential when no target references it.
func (s *Store) DeleteDeploymentCredential(ctx context.Context, id string) error {
	var used int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deployments WHERE credential_id=?`, id).Scan(&used); err != nil {
		return err
	}
	if used > 0 {
		return ErrConflict
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM deployment_credentials WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeploymentCredentialUsage returns how many targets reference a credential.
func (s *Store) DeploymentCredentialUsage(ctx context.Context, id string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deployments WHERE credential_id=?`, id).Scan(&n)
	return n, err
}
