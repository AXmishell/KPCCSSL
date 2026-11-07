package service

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/axmishell/kpccssl/internal/notify"
)

// Mail setting keys. The password is stored encrypted through the settings
// secret path.
const (
	SettingSMTPHost     = "smtp_host"
	SettingSMTPPort     = "smtp_port"
	SettingSMTPUsername = "smtp_username"
	SettingSMTPPassword = "smtp_password"
	SettingSMTPFrom     = "smtp_from"
	SettingSMTPTLS      = "smtp_tls"

	// Internal settings used to surface the last test-mail result.
	SettingMailLastTestAt = "mail_last_test_at"
	SettingMailLastTestOK = "mail_last_test_ok"
)

// ErrMailNotConfigured is returned when transactional mail cannot be sent
// because the SMTP settings are incomplete.
var ErrMailNotConfigured = errors.New("outbound email is not configured")

// ErrMailCredentialsUnavailable is returned when the stored SMTP password
// cannot be decrypted (for example after a master-key change).
var ErrMailCredentialsUnavailable = errors.New("outbound email credentials are unavailable")

// MailError wraps a transport-level delivery failure.
type MailError struct{ Err error }

func (e *MailError) Error() string { return "send email: " + e.Err.Error() }
func (e *MailError) Unwrap() error { return e.Err }

// MailConfig resolves and validates the transactional SMTP settings.
func (s *Service) MailConfig(ctx context.Context) (notify.SMTPConfig, error) {
	cfg := notify.SMTPConfig{
		Host:     strings.TrimSpace(s.SettingString(ctx, SettingSMTPHost, "")),
		Port:     s.SettingInt(ctx, SettingSMTPPort, 587),
		Username: strings.TrimSpace(s.SettingString(ctx, SettingSMTPUsername, "")),
		From:     strings.TrimSpace(s.SettingString(ctx, SettingSMTPFrom, "")),
		TLS:      strings.TrimSpace(s.SettingString(ctx, SettingSMTPTLS, "starttls")),
	}
	if v, err := s.GetSetting(ctx, SettingSMTPPassword); err != nil {
		return cfg, fmt.Errorf("%w: %v", ErrMailCredentialsUnavailable, err)
	} else if v != nil {
		if pw, ok := v.(string); ok {
			cfg.Password = pw
		}
	}
	if cfg.Host == "" || cfg.From == "" {
		return cfg, ErrMailNotConfigured
	}
	if err := notify.ValidateSMTPConfig(&cfg, true, false); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// MailConfigured reports whether enough settings exist to attempt delivery.
func (s *Service) MailConfigured(ctx context.Context) bool {
	_, err := s.MailConfig(ctx)
	return err == nil
}

// SendMail delivers one transactional message to a single recipient.
func (s *Service) SendMail(ctx context.Context, to, subject, body string) error {
	to = strings.TrimSpace(to)
	if to == "" {
		return errors.New("recipient is required")
	}
	rcpt, err := mail.ParseAddress(to)
	if err != nil {
		return errors.New("invalid recipient address")
	}
	to = rcpt.Address
	if strings.ContainsAny(to, "\r\n") {
		return errors.New("invalid recipient address")
	}
	cfg, err := s.MailConfig(ctx)
	if err != nil {
		return err
	}
	cfg.To = []string{to}
	sender := s.MailSender
	if sender == nil {
		sender = notify.SendSMTP
	}
	if err := sender(ctx, cfg, subject, body); err != nil {
		return &MailError{Err: err}
	}
	return nil
}

// TestMail sends a test message and surfaces configuration or transport
// errors to the administrator.
func (s *Service) TestMail(ctx context.Context, to string) error {
	err := s.SendMail(ctx, to, "KPCCSSL test email", "If you received this message, transactional email is configured correctly.")
	// Store the outcome for the settings page. Failures are non-fatal: the
	// original delivery error is what the caller needs to see.
	_ = s.SetSetting(ctx, SettingMailLastTestAt, time.Now().Unix(), false)
	_ = s.SetSetting(ctx, SettingMailLastTestOK, err == nil, false)
	return err
}

// ValidMailSetting reports whether a key/value pair is acceptable before it is
// persisted. Partial configurations are allowed while the form is filled in.
func ValidMailSetting(key string, value any) error {
	switch key {
	case SettingSMTPTLS:
		v, _ := value.(string)
		switch v {
		case "tls", "starttls", "none":
			return nil
		}
		return errors.New("SMTP TLS mode must be tls, starttls or none")
	case SettingSMTPPort:
		switch n := value.(type) {
		case float64:
			if n >= 1 && n <= 65535 {
				return nil
			}
		case int:
			if n >= 1 && n <= 65535 {
				return nil
			}
		case string:
			if n == "" {
				return nil
			}
		}
		return errors.New("SMTP port must be between 1 and 65535")
	case SettingSMTPHost, SettingSMTPUsername, SettingSMTPFrom:
		v, _ := value.(string)
		if strings.ContainsAny(v, "\r\n") {
			return errors.New("invalid mail setting")
		}
		if key == SettingSMTPFrom && v != "" {
			if _, err := mail.ParseAddress(v); err != nil {
				return errors.New("invalid sender address")
			}
		}
	}
	return nil
}
