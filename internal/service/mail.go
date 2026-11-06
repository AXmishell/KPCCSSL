package service

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"

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
)

// ErrMailNotConfigured is returned when transactional mail cannot be sent
// because the SMTP settings are incomplete.
var ErrMailNotConfigured = errors.New("outbound email is not configured")

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
	if v, err := s.GetSetting(ctx, SettingSMTPPassword); err == nil {
		if pw, ok := v.(string); ok {
			cfg.Password = pw
		}
	}
	if cfg.Host == "" || cfg.From == "" {
		return cfg, ErrMailNotConfigured
	}
	if cfg.Port <= 0 || cfg.Port > 65535 {
		return cfg, fmt.Errorf("invalid SMTP port")
	}
	from, err := mail.ParseAddress(cfg.From)
	if err != nil {
		return cfg, fmt.Errorf("invalid sender address")
	}
	// SMTPConfig uses From for both the envelope sender and the header, so keep
	// the bare address and reject header injection attempts.
	cfg.From = from.Address
	if strings.ContainsAny(cfg.From, "\r\n") {
		return cfg, fmt.Errorf("invalid sender address")
	}
	switch cfg.TLS {
	case "tls", "starttls", "none":
	default:
		return cfg, fmt.Errorf("SMTP TLS mode must be tls, starttls or none")
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
	return s.SendMail(ctx, to, "KPCCSSL test email", "If you received this message, transactional email is configured correctly.")
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
