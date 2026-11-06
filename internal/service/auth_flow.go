package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/axmishell/kpccssl/internal/auth"
	"github.com/axmishell/kpccssl/internal/id"
	"github.com/axmishell/kpccssl/internal/store"
)

// Token lifetimes. Password reset links are short-lived; email verification
// links may be opened later because the user has to switch mailboxes.
const (
	PasswordResetTTL = 30 * time.Minute
	EmailChangeTTL   = 24 * time.Hour
)

// ErrInvalidToken is returned when a reset or confirmation token is unknown,
// expired, already used, or used for the wrong purpose.
var ErrInvalidToken = errors.New("link is invalid or has expired")

// InputError marks user-correctable validation failures so the API can return
// 422 instead of a generic 400.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

func inputError(err error) error {
	if err == nil {
		return nil
	}
	return &InputError{Msg: err.Error()}
}

func (s *Service) issueAuthToken(ctx context.Context, userID, purpose, email string, ttl time.Duration) (string, error) {
	raw, err := auth.NewToken()
	if err != nil {
		return "", err
	}
	now := store.Now()
	_ = s.Store.DeleteExpiredAuthTokens(ctx, now)
	if err := s.Store.DeleteAuthTokens(ctx, userID, purpose); err != nil {
		return "", err
	}
	t := &store.AuthToken{
		ID: id.New(), UserID: userID, Purpose: purpose, TokenHash: auth.HashToken(raw),
		ExpiresAt: now + int64(ttl/time.Second), CreatedAt: now,
	}
	if email != "" {
		t.Email = &email
	}
	if err := s.Store.CreateAuthToken(ctx, t); err != nil {
		return "", err
	}
	return raw, nil
}

func authLink(base, path, token string) string {
	return strings.TrimRight(base, "/") + path + "?token=" + url.QueryEscape(token)
}

// RequestPasswordReset sends a reset link when the account exists. Unknown and
// disabled accounts are silently ignored to avoid user enumeration.
func (s *Service) RequestPasswordReset(ctx context.Context, email, linkBase string) error {
	if _, err := s.MailConfig(ctx); err != nil {
		return err
	}
	email = normalizeEmail(strings.TrimSpace(email))
	if validateEmail(email) != nil {
		return nil
	}
	u, err := s.Store.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return err
	}
	if u.Disabled {
		return nil
	}
	raw, err := s.issueAuthToken(ctx, u.ID, "password_reset", "", PasswordResetTTL)
	if err != nil {
		return err
	}
	link := authLink(linkBase, "/reset-password", raw)
	subject, body := passwordResetMail(link)
	// Send asynchronously so response latency cannot reveal whether the
	// account exists.
	s.sendMailAsync(u.Email, subject, body)
	return nil
}

// ResetPassword redeems a password-reset token and signs the user out of every
// session. The token is consumed before the hash is written so parallel
// redemption can only succeed once.
func (s *Service) ResetPassword(ctx context.Context, token, newPassword string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return ErrInvalidToken
	}
	t, err := s.Store.GetAuthTokenByHash(ctx, auth.HashToken(token))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrInvalidToken
		}
		return err
	}
	now := store.Now()
	if t.Purpose != "password_reset" || t.UsedAt != nil || t.ExpiresAt < now {
		return ErrInvalidToken
	}
	u, err := s.Store.GetUser(ctx, t.UserID)
	if err != nil {
		return ErrInvalidToken
	}
	if u.Disabled {
		return ErrInvalidToken
	}
	if err := auth.ValidatePasswordPolicy(newPassword, u.Email); err != nil {
		return inputError(err)
	}
	ok, err := s.Store.ConsumeAuthToken(ctx, t.ID, now)
	if err != nil {
		return err
	}
	if !ok {
		return ErrInvalidToken
	}
	hash, err := auth.HashPassword(newPassword)
	if err != nil {
		return err
	}
	if err := s.Store.UpdateUserPassword(ctx, u.ID, hash, now); err != nil {
		return err
	}
	_ = s.Store.DeleteUserSessions(ctx, u.ID, "")
	_ = s.Store.DeleteAuthTokens(ctx, u.ID, "password_reset")
	return nil
}

// RequestEmailChange verifies the current password and sends a confirmation
// link to the new address. The account keeps its old address until the link is
// opened.
func (s *Service) RequestEmailChange(ctx context.Context, userID, newEmail, password, linkBase string) error {
	u, err := s.Store.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	if !auth.VerifyPassword(u.PasswordHash, password) {
		return ErrUnauthorized
	}
	newEmail = normalizeEmail(strings.TrimSpace(newEmail))
	if err := validateEmail(newEmail); err != nil {
		return inputError(err)
	}
	if newEmail == u.Email {
		return &InputError{Msg: "new email must be different from the current email"}
	}
	if existing, err := s.Store.GetUserByEmail(ctx, newEmail); err == nil && existing.ID != u.ID {
		return &ConflictError{Msg: "email is already registered"}
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if _, err := s.MailConfig(ctx); err != nil {
		return err
	}
	raw, err := s.issueAuthToken(ctx, u.ID, "email_change", newEmail, EmailChangeTTL)
	if err != nil {
		return err
	}
	link := authLink(linkBase, "/confirm-email", raw)
	subject, body := emailChangeMail(link)
	return s.SendMail(ctx, newEmail, subject, body)
}

// ConfirmEmailChange redeems an email-change token, applies the new address and
// invalidates every session so the user signs in with the verified address.
func (s *Service) ConfirmEmailChange(ctx context.Context, token string) (*store.User, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrInvalidToken
	}
	t, err := s.Store.GetAuthTokenByHash(ctx, auth.HashToken(token))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrInvalidToken
		}
		return nil, err
	}
	now := store.Now()
	if t.Purpose != "email_change" || t.UsedAt != nil || t.ExpiresAt < now || t.Email == nil {
		return nil, ErrInvalidToken
	}
	u, err := s.Store.GetUser(ctx, t.UserID)
	if err != nil || u.Disabled {
		return nil, ErrInvalidToken
	}
	newEmail := normalizeEmail(strings.TrimSpace(*t.Email))
	if err := validateEmail(newEmail); err != nil {
		return nil, ErrInvalidToken
	}
	if existing, err := s.Store.GetUserByEmail(ctx, newEmail); err == nil && existing.ID != u.ID {
		return nil, &ConflictError{Msg: "email is already registered"}
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	ok, err := s.Store.ConsumeAuthToken(ctx, t.ID, now)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrInvalidToken
	}
	if err := s.Store.UpdateUserEmail(ctx, u.ID, newEmail, now); err != nil {
		return nil, err
	}
	_ = s.Store.DeleteAuthTokens(ctx, u.ID, "email_change")
	_ = s.Store.DeleteUserSessions(ctx, u.ID, "")
	return s.Store.GetUser(ctx, u.ID)
}

// AdminSendPasswordReset lets an administrator trigger the same reset mail
// without knowing the user's current password.
func (s *Service) AdminSendPasswordReset(ctx context.Context, userID, linkBase string) error {
	u, err := s.Store.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	if u.Disabled {
		return &InputError{Msg: "user is disabled"}
	}
	if _, err := s.MailConfig(ctx); err != nil {
		return err
	}
	raw, err := s.issueAuthToken(ctx, u.ID, "password_reset", "", PasswordResetTTL)
	if err != nil {
		return err
	}
	link := authLink(linkBase, "/reset-password", raw)
	subject, body := passwordResetMail(link)
	return s.SendMail(ctx, u.Email, subject, body)
}

func (s *Service) sendMailAsync(to, subject, body string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.SendMail(ctx, to, subject, body); err != nil {
			slog.Warn("transactional email failed", "error", err)
		}
	}()
}

func passwordResetMail(link string) (string, string) {
	body := fmt.Sprintf(`KPCCSSL password reset / 密码重置

Open the link below within 30 minutes to set a new password:
请在 30 分钟内打开以下链接设置新密码：

%s

If you did not request this, ignore this email. Your password is unchanged.
如果这不是你发起的请求，请忽略此邮件，密码不会被修改。
`, link)
	return "KPCCSSL password reset / 密码重置", body
}

func emailChangeMail(link string) (string, string) {
	body := fmt.Sprintf(`KPCCSSL email verification / 邮箱验证

Open the link below within 24 hours to confirm your new email address:
请在 24 小时内打开以下链接确认新的邮箱地址：

%s

If you did not request this, ignore this email. Your address is unchanged.
如果这不是你发起的请求，请忽略此邮件，邮箱不会被修改。
`, link)
	return "KPCCSSL email verification / 邮箱验证", body
}
