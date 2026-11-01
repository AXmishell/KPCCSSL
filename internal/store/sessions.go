package store

import (
	"context"
	"database/sql"
)

// CreateSession stores a hashed session token.
func (s *Store) CreateSession(ctx context.Context, sess *Session) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions
		(id,user_id,token_hash,csrf_secret,expires_at,created_at,last_seen_at,ip,user_agent)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		sess.ID, sess.UserID, sess.TokenHash, sess.CSRFSecret, sess.ExpiresAt, sess.CreatedAt, sess.LastSeenAt, sess.IP, sess.UserAgent)
	return err
}

// GetSessionByTokenHash returns a session by its SHA-256 token hash.
func (s *Store) GetSessionByTokenHash(ctx context.Context, hash []byte) (*Session, error) {
	var sess Session
	var ip, ua sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id,user_id,token_hash,csrf_secret,expires_at,created_at,last_seen_at,ip,user_agent
		FROM sessions WHERE token_hash = ?`, hash).Scan(
		&sess.ID, &sess.UserID, &sess.TokenHash, &sess.CSRFSecret, &sess.ExpiresAt, &sess.CreatedAt, &sess.LastSeenAt, &ip, &ua)
	if err != nil {
		return nil, notFound(err)
	}
	sess.IP, sess.UserAgent = ip.String, ua.String
	return &sess, nil
}

// TouchSession extends a session expiry.
func (s *Store) TouchSession(ctx context.Context, id string, expiresAt, lastSeen int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET expires_at=?, last_seen_at=? WHERE id=?`, expiresAt, lastSeen, id)
	return err
}

// DeleteSession removes one session.
func (s *Store) DeleteSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id=?`, id)
	return err
}

// DeleteUserSessions removes every session of a user, optionally keeping one.
func (s *Store) DeleteUserSessions(ctx context.Context, userID, keepID string) error {
	if keepID != "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=? AND id<>?`, userID, keepID)
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, userID)
	return err
}

// CleanupSessions deletes expired sessions.
func (s *Store) CleanupSessions(ctx context.Context, before int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
