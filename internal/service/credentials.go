package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/axmishell/kpccssl/internal/deploy"
	"github.com/axmishell/kpccssl/internal/id"
	"github.com/axmishell/kpccssl/internal/store"
)

// DeploymentCredentialInput is the create/update payload. Config may be
// omitted on update to keep the stored secrets.
type DeploymentCredentialInput struct {
	Name   string          `json:"name"`
	Type   string          `json:"type"`
	Config json.RawMessage `json:"config"`
}

// CreateDeploymentCredential validates and stores a reusable credential.
func (s *Service) CreateDeploymentCredential(ctx context.Context, in DeploymentCredentialInput) (*store.DeploymentCredential, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, errors.New("name is required")
	}
	in.Type = strings.TrimSpace(in.Type)
	if err := deploy.ValidateCredential(in.Type, string(in.Config)); err != nil {
		return nil, err
	}
	now := store.Now()
	c := &store.DeploymentCredential{
		ID: id.New(), Name: in.Name, Type: in.Type,
		KeyVersion: s.Box.Version(), CreatedAt: now, UpdatedAt: now,
	}
	enc, err := s.Box.Encrypt(in.Config, deploy.CredentialAAD(c.ID))
	if err != nil {
		return nil, err
	}
	c.ConfigEnc = enc
	if err := s.Store.CreateDeploymentCredential(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// UpdateDeploymentCredential updates a credential. Config is merged with the
// existing values so editing only the name does not erase secrets.
func (s *Service) UpdateDeploymentCredential(ctx context.Context, id string, in DeploymentCredentialInput) (*store.DeploymentCredential, error) {
	c, err := s.Store.GetDeploymentCredential(ctx, id)
	if err != nil {
		return nil, err
	}
	if in.Name != "" {
		c.Name = strings.TrimSpace(in.Name)
	}
	if in.Type != "" && in.Type != c.Type {
		return nil, errors.New("credential type cannot be changed")
	}
	if len(in.Config) > 0 {
		merged, err := s.mergeCredentialConfig(c, in.Config)
		if err != nil {
			return nil, err
		}
		if err := deploy.ValidateCredential(c.Type, string(merged)); err != nil {
			return nil, err
		}
		enc, err := s.Box.Encrypt(merged, deploy.CredentialAAD(c.ID))
		if err != nil {
			return nil, err
		}
		c.ConfigEnc, c.KeyVersion = enc, s.Box.Version()
	}
	c.UpdatedAt = store.Now()
	if err := s.Store.UpdateDeploymentCredential(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// DeleteDeploymentCredential removes a credential unless a target uses it.
func (s *Service) DeleteDeploymentCredential(ctx context.Context, id string) error {
	n, err := s.Store.DeploymentCredentialUsage(ctx, id)
	if err != nil {
		return err
	}
	if n > 0 {
		return &ConflictError{Msg: "credential is still used by deployment targets"}
	}
	return s.Store.DeleteDeploymentCredential(ctx, id)
}

// TestDeploymentCredential validates an unsaved credential config.
func (s *Service) TestDeploymentCredential(ctx context.Context, credType string, config json.RawMessage) error {
	if strings.TrimSpace(credType) == "" {
		return errors.New("type is required")
	}
	if len(config) == 0 {
		return errors.New("config is required")
	}
	return deploy.TestCredential(ctx, credType, string(config))
}

// TestDeploymentCredentialByID checks a saved credential.
func (s *Service) TestDeploymentCredentialByID(ctx context.Context, id string) error {
	c, err := s.Store.GetDeploymentCredential(ctx, id)
	if err != nil {
		return err
	}
	plain, err := s.Box.Decrypt(c.ConfigEnc, deploy.CredentialAAD(c.ID))
	if err != nil {
		return err
	}
	return deploy.TestCredential(ctx, c.Type, string(plain))
}

// DecryptDeploymentCredentialConfig returns the stored config for masks and
// validation; callers must not expose secret values.
func (s *Service) DecryptDeploymentCredentialConfig(c *store.DeploymentCredential) (map[string]any, error) {
	plain, err := s.Box.Decrypt(c.ConfigEnc, deploy.CredentialAAD(c.ID))
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(plain, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) mergeCredentialConfig(c *store.DeploymentCredential, patch json.RawMessage) ([]byte, error) {
	existing := map[string]json.RawMessage{}
	if plain, err := s.Box.Decrypt(c.ConfigEnc, deploy.CredentialAAD(c.ID)); err == nil {
		_ = json.Unmarshal(plain, &existing)
	}
	var update map[string]json.RawMessage
	if err := json.Unmarshal(patch, &update); err != nil {
		return nil, errors.New("invalid config")
	}
	for k, v := range update {
		existing[k] = v
	}
	// Switching SSH authentication methods must not leave both secrets active:
	// the deployer prefers a private key when present.
	if c.Type == deploy.CredentialSSH {
		if raw, ok := update["password"]; ok && jsonStringNotEmpty(raw) {
			delete(existing, "private_key")
			delete(existing, "passphrase")
		}
		if raw, ok := update["private_key"]; ok && jsonStringNotEmpty(raw) {
			delete(existing, "password")
		}
	}
	return json.Marshal(existing)
}

func jsonStringNotEmpty(raw json.RawMessage) bool {
	var s string
	return json.Unmarshal(raw, &s) == nil && strings.TrimSpace(s) != ""
}
