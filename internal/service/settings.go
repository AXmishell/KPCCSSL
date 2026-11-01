package service

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/axmishell/kpccssl/internal/store"
)

// GetSetting returns a decoded setting value, or nil when absent.
func (s *Service) GetSetting(ctx context.Context, key string) (any, error) {
	vj, ve, isSecret, _, _, err := s.Store.GetSetting(ctx, key)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if isSecret {
		if ve == nil {
			return nil, nil
		}
		plain, err := s.Box.Decrypt(*ve, "settings:"+key+":value")
		if err != nil {
			return nil, err
		}
		var out any
		_ = json.Unmarshal(plain, &out)
		return out, nil
	}
	var out any
	if vj != nil {
		_ = json.Unmarshal([]byte(*vj), &out)
	}
	return out, nil
}

// SetSetting stores a JSON value, encrypting it when secret is true.
func (s *Service) SetSetting(ctx context.Context, key string, value any, secret bool) error {
	if key == "" || len(key) > 128 {
		return errors.New("invalid setting key")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if secret {
		enc, err := s.Box.Encrypt(raw, "settings:"+key+":value")
		if err != nil {
			return err
		}
		return s.Store.SetSetting(ctx, key, nil, &enc, true, s.Box.Version(), store.Now())
	}
	str := string(raw)
	return s.Store.SetSetting(ctx, key, &str, nil, false, s.Box.Version(), store.Now())
}

// SettingsView returns all settings with secret values masked.
func (s *Service) SettingsView(ctx context.Context) (map[string]any, error) {
	keys, err := s.Store.ListSettingsKeys(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	for k, secret := range keys {
		if secret {
			out[k] = "********"
			continue
		}
		v, err := s.GetSetting(ctx, k)
		if err == nil {
			out[k] = v
		}
	}
	return out, nil
}
