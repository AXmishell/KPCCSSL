package store

import (
	"context"
	"database/sql"
)

// AuthToken is a single-use token for password reset or email confirmation.
// TokenHash is the SHA-256 digest of the raw token; the raw value is never
// persisted.
type AuthToken struct {
	ID        string
	UserID    string
	Purpose   string
	TokenHash []byte
	Email     *string
	ExpiresAt int64
	UsedAt    *int64
	CreatedAt int64
}

// CreateAuthToken inserts a token.
func (s *Store) CreateAuthToken(ctx context.Context, t *AuthToken) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO auth_tokens
		(id,user_id,purpose,token_hash,email,expires_at,used_at,created_at)
		VALUES (?,?,?,?,?,?,?,?)`,
		t.ID, t.UserID, t.Purpose, t.TokenHash, t.Email, t.ExpiresAt, t.UsedAt, t.CreatedAt)
	return err
}

// GetAuthTokenByHash looks up a token by its SHA-256 digest.
func (s *Store) GetAuthTokenByHash(ctx context.Context, hash []byte) (*AuthToken, error) {
	var t AuthToken
	var email sql.NullString
	var used sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT id,user_id,purpose,token_hash,email,expires_at,used_at,created_at
		FROM auth_tokens WHERE token_hash = ?`, hash).Scan(
		&t.ID, &t.UserID, &t.Purpose, &t.TokenHash, &email, &t.ExpiresAt, &used, &t.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	if email.Valid {
		t.Email = &email.String
	}
	if used.Valid {
		t.UsedAt = &used.Int64
	}
	return &t, nil
}

// ConsumeAuthToken marks a token as used. It returns false when the token was
// already consumed, which makes parallel redemption safe.
func (s *Store) ConsumeAuthToken(ctx context.Context, id string, now int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE auth_tokens SET used_at=? WHERE id=? AND used_at IS NULL`, now, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// DeleteAuthTokens removes every token of a user for one purpose.
func (s *Store) DeleteAuthTokens(ctx context.Context, userID, purpose string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM auth_tokens WHERE user_id=? AND purpose=?`, userID, purpose)
	return err
}

// DeleteExpiredAuthTokens prunes tokens that can no longer be redeemed.
func (s *Store) DeleteExpiredAuthTokens(ctx context.Context, before int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM auth_tokens WHERE expires_at < ?`, before)
	return err
}
