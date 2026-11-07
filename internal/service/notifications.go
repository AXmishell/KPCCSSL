package service

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/axmishell/kpccssl/internal/id"
	"github.com/axmishell/kpccssl/internal/store"
)

// NotificationInput is the channel payload. Config may be omitted on update to
// keep the stored secret.
type NotificationInput struct {
	Name    string          `json:"name"`
	Type    string          `json:"type"`
	Config  json.RawMessage `json:"config"`
	Events  []string        `json:"events"`
	Enabled bool            `json:"enabled"`
}

// SupportedEvents lists subscribable notification events.
func SupportedEvents() []string {
	return []string{
		"*",
		"certificate.issued",
		"certificate.renewed",
		"certificate.expiring",
		"certificate.failed",
		"certificate.renew_failed",
		"certificate.revoked",
		"ca.registered",
		"deployment.failed",
		"workflow.notify",
	}
}

// NotificationView is a channel without secrets.
type NotificationView struct {
	ID                 string         `json:"id"`
	Name               string         `json:"name"`
	Type               string         `json:"type"`
	Events             []string       `json:"events"`
	Enabled            bool           `json:"enabled"`
	PublicConfig       map[string]any `json:"public_config"`
	ConfiguredFields   []string       `json:"configured_fields"`
	LastDeliveryStatus string         `json:"last_delivery_status,omitempty"`
	LastDeliveryAt     *int64         `json:"last_delivery_at,omitempty"`
	LastDeliveryError  *string        `json:"last_delivery_error,omitempty"`
	CreatedAt          int64          `json:"created_at"`
	UpdatedAt          int64          `json:"updated_at"`
}

// NotificationChannelView converts a row to its public representation. Secret
// fields are omitted and only names are exposed through configured_fields.
func (s *Service) NotificationChannelView(ctx context.Context, c store.NotificationChannel) NotificationView {
	var events []string
	_ = json.Unmarshal([]byte(c.EventsJSON), &events)
	if events == nil {
		events = []string{}
	}
	public, configured := s.notificationPublicConfig(c)
	v := NotificationView{
		ID: c.ID, Name: c.Name, Type: c.Type, Events: events, Enabled: c.Enabled,
		PublicConfig: public, ConfiguredFields: configured,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
	if status, at, lastErr, err := s.Store.LastNotificationDelivery(ctx, c.ID); err == nil && status != "" {
		v.LastDeliveryStatus, v.LastDeliveryAt, v.LastDeliveryError = status, at, lastErr
	}
	return v
}

// notificationPublicConfig returns non-secret fields and the names of fields
// that are configured. It never exposes a secret value.
func (s *Service) notificationPublicConfig(c store.NotificationChannel) (map[string]any, []string) {
	public := map[string]any{}
	configured := []string{}
	plain, err := s.Box.Decrypt(c.ConfigEnc, "notification_channels:"+c.ID+":config")
	if err != nil {
		return public, configured
	}
	var cfg map[string]any
	if err := json.Unmarshal(plain, &cfg); err != nil {
		return public, configured
	}
	for k, v := range cfg {
		if v == nil {
			continue
		}
		if s, ok := v.(string); ok && strings.TrimSpace(s) == "" {
			continue
		}
		configured = append(configured, k)
	}
	sort.Strings(configured)
	switch c.Type {
	case "smtp":
		for _, k := range []string{"use_system_mail", "host", "port", "tls", "username", "from", "to"} {
			if v, ok := cfg[k]; ok {
				public[k] = v
			}
		}
	case "webhook":
		if v, ok := cfg["url"]; ok {
			public["url"] = v
		}
	case "telegram":
		if v, ok := cfg["chat_id"]; ok {
			public["chat_id"] = v
		}
	case "feishu", "dingtalk", "wecom":
		if v, ok := cfg["webhook_url"]; ok {
			public["webhook_url"] = v
		}
	}
	return public, configured
}

// CreateNotification validates and stores a channel.
func (s *Service) CreateNotification(ctx context.Context, in NotificationInput) (*store.NotificationChannel, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Type = strings.TrimSpace(in.Type)
	if in.Name == "" {
		return nil, errors.New("name is required")
	}
	if len(in.Config) == 0 {
		return nil, errors.New("config is required")
	}
	normalized, err := s.Notifier.NormalizeConfig(in.Type, in.Config)
	if err != nil {
		return nil, err
	}
	if err := validateEvents(in.Events); err != nil {
		return nil, err
	}
	events, _ := json.Marshal(in.Events)
	now := store.Now()
	c := &store.NotificationChannel{
		ID: id.New(), Name: in.Name, Type: in.Type, EventsJSON: string(events),
		Enabled: in.Enabled, KeyVersion: s.Box.Version(), CreatedAt: now, UpdatedAt: now,
	}
	enc, err := s.Box.Encrypt(normalized, "notification_channels:"+c.ID+":config")
	if err != nil {
		return nil, err
	}
	c.ConfigEnc = enc
	if err := s.Store.CreateNotificationChannel(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// UpdateNotification updates a channel. Config is merged with the existing
// values so editing only the name does not erase secrets.
func (s *Service) UpdateNotification(ctx context.Context, id string, in NotificationInput) (*store.NotificationChannel, error) {
	c, err := s.Store.GetNotificationChannel(ctx, id)
	if err != nil {
		return nil, err
	}
	if in.Name != "" {
		c.Name = strings.TrimSpace(in.Name)
	}
	if in.Type != "" && in.Type != c.Type {
		return nil, errors.New("channel type cannot be changed")
	}
	if len(in.Config) > 0 {
		merged, err := s.mergeNotificationConfig(c, in.Config)
		if err != nil {
			return nil, err
		}
		normalized, err := s.Notifier.NormalizeConfig(c.Type, merged)
		if err != nil {
			return nil, err
		}
		enc, err := s.Box.Encrypt(normalized, "notification_channels:"+c.ID+":config")
		if err != nil {
			return nil, err
		}
		c.ConfigEnc, c.KeyVersion = enc, s.Box.Version()
	}
	if in.Events != nil {
		if err := validateEvents(in.Events); err != nil {
			return nil, err
		}
		events, _ := json.Marshal(in.Events)
		c.EventsJSON = string(events)
	}
	c.Enabled = in.Enabled
	c.UpdatedAt = store.Now()
	if err := s.Store.UpdateNotificationChannel(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Service) mergeNotificationConfig(c *store.NotificationChannel, patch json.RawMessage) ([]byte, error) {
	existing := map[string]json.RawMessage{}
	if plain, err := s.Box.Decrypt(c.ConfigEnc, "notification_channels:"+c.ID+":config"); err == nil {
		_ = json.Unmarshal(plain, &existing)
	}
	var update map[string]json.RawMessage
	if err := json.Unmarshal(patch, &update); err != nil {
		return nil, errors.New("invalid config")
	}
	for k, v := range update {
		existing[k] = v
	}
	return json.Marshal(existing)
}

// NotificationError marks a failed test delivery. The handler maps it to a
// generic 502 while the audit log keeps the redacted detail.
type NotificationError struct{ Err error }

func (e *NotificationError) Error() string { return "send notification: " + e.Err.Error() }
func (e *NotificationError) Unwrap() error { return e.Err }

// DeleteNotification removes a channel.
func (s *Service) DeleteNotification(ctx context.Context, id string) error {
	return s.Store.DeleteNotificationChannel(ctx, id)
}

func validateEvents(events []string) error {
	allowed := map[string]bool{}
	for _, e := range SupportedEvents() {
		allowed[e] = true
	}
	for _, e := range events {
		if !allowed[e] {
			return errors.New("unsupported event: " + e)
		}
	}
	return nil
}
