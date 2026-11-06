package service

import (
	"context"
	"encoding/json"
	"errors"
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
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	Events    []string `json:"events"`
	Enabled   bool     `json:"enabled"`
	CreatedAt int64    `json:"created_at"`
	UpdatedAt int64    `json:"updated_at"`
}

// NotificationChannelView converts a row to its public representation.
func NotificationChannelView(c store.NotificationChannel) NotificationView {
	var events []string
	_ = json.Unmarshal([]byte(c.EventsJSON), &events)
	if events == nil {
		events = []string{}
	}
	return NotificationView{ID: c.ID, Name: c.Name, Type: c.Type, Events: events, Enabled: c.Enabled, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
}

// CreateNotification validates and stores a channel.
func (s *Service) CreateNotification(ctx context.Context, in NotificationInput) (*store.NotificationChannel, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, errors.New("name is required")
	}
	if len(in.Config) == 0 {
		return nil, errors.New("config is required")
	}
	if err := s.Notifier.Validate(in.Type, string(in.Config)); err != nil {
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
	enc, err := s.Box.Encrypt(in.Config, "notification_channels:"+c.ID+":config")
	if err != nil {
		return nil, err
	}
	c.ConfigEnc = enc
	if err := s.Store.CreateNotificationChannel(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// UpdateNotification updates a channel.
func (s *Service) UpdateNotification(ctx context.Context, id string, in NotificationInput) (*store.NotificationChannel, error) {
	c, err := s.Store.GetNotificationChannel(ctx, id)
	if err != nil {
		return nil, err
	}
	if in.Name != "" {
		c.Name = strings.TrimSpace(in.Name)
	}
	if in.Type != "" {
		c.Type = in.Type
	}
	if len(in.Config) > 0 {
		if err := s.Notifier.Validate(c.Type, string(in.Config)); err != nil {
			return nil, err
		}
		enc, err := s.Box.Encrypt(in.Config, "notification_channels:"+c.ID+":config")
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
