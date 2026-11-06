package store

import (
	"context"
	"database/sql"
	"fmt"
)

// CreateUser inserts a user.
func (s *Store) CreateUser(ctx context.Context, u *User) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO users
		(id,email,password_hash,totp_secret_enc,role,disabled,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?)`,
		u.ID, u.Email, u.PasswordHash, u.TOTPSecretEnc, u.Role, boolInt(u.Disabled), u.CreatedAt, u.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	return nil
}

// CountUsers returns the number of users (setup is allowed only when zero).
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// CountActiveAdmins returns enabled administrators.
func (s *Store) CountActiveAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role='admin' AND disabled=0`).Scan(&n)
	return n, err
}

// GetUserByEmail looks up a user by email.
func (s *Store) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx, userCols+` WHERE email = ?`, email))
}

// GetUser looks up a user by id.
func (s *Store) GetUser(ctx context.Context, id string) (*User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx, userCols+` WHERE id = ?`, id))
}

// ListUsers returns all users.
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, userCols+` ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := s.scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// UpdateUserPassword replaces the password hash and bumps updated_at.
func (s *Store) UpdateUserPassword(ctx context.Context, id, hash string, now int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash=?, updated_at=? WHERE id=?`, hash, now, id)
	return err
}

// UpdateUserEmail changes the login address after it has been verified.
func (s *Store) UpdateUserEmail(ctx context.Context, id, email string, now int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET email=?, updated_at=? WHERE id=?`, email, now, id)
	return err
}

// SetUserTOTP stores (or clears) the encrypted TOTP secret.
func (s *Store) SetUserTOTP(ctx context.Context, id string, enc *string, now int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET totp_secret_enc=?, updated_at=? WHERE id=?`, enc, now, id)
	return err
}

// UpdateUserMeta updates role and disabled flag.
func (s *Store) UpdateUserMeta(ctx context.Context, id, role string, disabled bool, now int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET role=?, disabled=?, updated_at=? WHERE id=?`, role, boolInt(disabled), now, id)
	return err
}

const userCols = `SELECT id,email,password_hash,totp_secret_enc,role,disabled,created_at,updated_at FROM users`

func (s *Store) scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	var totp sql.NullString
	var disabled int
	if err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &totp, &u.Role, &disabled, &u.CreatedAt, &u.UpdatedAt); err != nil {
		return nil, notFound(err)
	}
	if totp.Valid {
		u.TOTPSecretEnc = &totp.String
	}
	u.Disabled = disabled != 0
	return &u, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
