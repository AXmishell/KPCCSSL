package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/axmishell/kpccssl/internal/id"
	"github.com/axmishell/kpccssl/internal/store"
)

// SessionTTL is the sliding session lifetime.
const SessionTTL = 12 * time.Hour

// Principal is the authenticated request identity.
type Principal struct {
	User    *store.User
	Session *store.Session
	CSRF    string
}

// Manager creates and resolves sessions.
type Manager struct {
	Store      *store.Store
	CookieName string
	Secure     bool
	TTL        time.Duration
}

// NewManager builds a session manager.
func NewManager(st *store.Store, cookieName string, secure bool) *Manager {
	return &Manager{Store: st, CookieName: cookieName, Secure: secure, TTL: SessionTTL}
}

// Create starts a session and returns the raw token, the CSRF token and the row.
func (m *Manager) Create(ctx context.Context, user *store.User, ip, ua string) (token, csrf string, sess *store.Session, err error) {
	raw, err := randomToken(32)
	if err != nil {
		return "", "", nil, err
	}
	csrfSecret, err := randomToken(32)
	if err != nil {
		return "", "", nil, err
	}
	now := store.Now()
	sum := sha256.Sum256([]byte(raw))
	sess = &store.Session{
		ID:         id.New(),
		UserID:     user.ID,
		TokenHash:  sum[:],
		CSRFSecret: csrfSecret,
		ExpiresAt:  now + int64(m.TTL/time.Second),
		CreatedAt:  now,
		LastSeenAt: now,
		IP:         ip,
		UserAgent:  ua,
	}
	if err := m.Store.CreateSession(ctx, sess); err != nil {
		return "", "", nil, err
	}
	return raw, csrfToken(sess), sess, nil
}

// Resolve loads a principal from a raw session token.
func (m *Manager) Resolve(ctx context.Context, token string) (*Principal, error) {
	if token == "" {
		return nil, store.ErrNotFound
	}
	sum := sha256.Sum256([]byte(token))
	sess, err := m.Store.GetSessionByTokenHash(ctx, sum[:])
	if err != nil {
		return nil, err
	}
	now := store.Now()
	if sess.ExpiresAt < now {
		_ = m.Store.DeleteSession(ctx, sess.ID)
		return nil, store.ErrNotFound
	}
	user, err := m.Store.GetUser(ctx, sess.UserID)
	if err != nil {
		return nil, err
	}
	if user.Disabled {
		return nil, errors.New("user disabled")
	}
	// Sliding expiry, refreshed at most every 5 minutes.
	if now-sess.LastSeenAt > 300 {
		expires := now + int64(m.TTL/time.Second)
		if err := m.Store.TouchSession(ctx, sess.ID, expires, now); err == nil {
			sess.ExpiresAt, sess.LastSeenAt = expires, now
		}
	}
	return &Principal{User: user, Session: sess, CSRF: csrfToken(sess)}, nil
}

// Destroy deletes one session.
func (m *Manager) Destroy(ctx context.Context, sessionID string) error {
	return m.Store.DeleteSession(ctx, sessionID)
}

// VerifyCSRF compares the submitted token with the token derived from the
// server-side session secret (double-submit + origin check).
func (m *Manager) VerifyCSRF(p *Principal, submitted string) bool {
	if p == nil || submitted == "" {
		return false
	}
	return hmac.Equal([]byte(submitted), []byte(csrfToken(p.Session)))
}

func csrfToken(sess *store.Session) string {
	mac := hmac.New(sha256.New, []byte(sess.CSRFSecret))
	mac.Write([]byte("csrf:" + sess.ID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

type principalKey struct{}

// WithPrincipal stores a principal in the context.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// FromContext returns the authenticated principal, if any.
func FromContext(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey{}).(*Principal)
	return p
}
