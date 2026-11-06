package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Deployment credential types. Each type maps to one or more deployment
// targets and stores only the authentication fields those targets need.
const (
	CredentialTencent  = "tencent"
	CredentialAliyun   = "aliyun"
	CredentialQiniu    = "qiniu"
	CredentialBaota    = "baota"
	CredentialOnePanel = "1panel"
	CredentialSSH      = "ssh"
)

// DeploymentCredentialTypes lists every supported credential type.
func DeploymentCredentialTypes() []string {
	return []string{CredentialTencent, CredentialAliyun, CredentialQiniu, CredentialBaota, CredentialOnePanel, CredentialSSH}
}

// CredentialAAD binds a credential ciphertext to its record and field.
func CredentialAAD(id string) string { return "deployment_credentials:" + id + ":config" }

var credentialFieldsByType = map[string][]string{
	CredentialTencent:  {"secret_id", "secret_key"},
	CredentialAliyun:   {"access_key_id", "access_key_secret"},
	CredentialQiniu:    {"access_key", "access_secret"},
	CredentialBaota:    {"url", "api_key", "ignore_tls"},
	CredentialOnePanel: {"url", "api_key", "version", "ignore_tls"},
	CredentialSSH: {
		"host", "port", "user", "password", "private_key", "passphrase",
		"host_key_sha256", "insecure_ignore_host_key",
	},
}

// CredentialFields returns the allowed fields for a credential type.
func CredentialFields(credType string) ([]string, bool) {
	fields, ok := credentialFieldsByType[credType]
	if !ok {
		return nil, false
	}
	return append([]string(nil), fields...), true
}

// CredentialTypeForDeployment maps a deployment target to its credential type.
func CredentialTypeForDeployment(depType string) (string, bool) {
	switch depType {
	case "tencent_cdn", "tencent_waf", "tencent_cos", "tencent_teo":
		return CredentialTencent, true
	case "aliyun_cdn", "aliyun_waf", "aliyun_oss", "aliyun_dcdn":
		return CredentialAliyun, true
	case "qiniu_cdn":
		return CredentialQiniu, true
	case "baota_panel", "baota_site":
		return CredentialBaota, true
	case "1panel_panel", "1panel_site":
		return CredentialOnePanel, true
	case "ssh":
		return CredentialSSH, true
	}
	return "", false
}

// ValidateCredential checks a credential config before it is stored.
func ValidateCredential(credType, configJSON string) error {
	switch credType {
	case CredentialTencent:
		var cfg TencentConfig
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		return cfg.validateAuth()
	case CredentialAliyun:
		var cfg AliyunConfig
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		return cfg.validateAuth()
	case CredentialQiniu:
		var cfg QiniuConfig
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		return cfg.validateAuth()
	case CredentialBaota:
		var cfg BaotaConfig
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		return cfg.validate(false)
	case CredentialOnePanel:
		var cfg OnePanelConfig
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		return cfg.validate(false)
	case CredentialSSH:
		var cfg SSHConfig
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		return cfg.validateAuth()
	}
	return errors.New("unsupported credential type")
}

// TestCredential performs a read-only connectivity check for a credential.
func TestCredential(ctx context.Context, credType, configJSON string) error {
	switch credType {
	case CredentialTencent:
		var cfg TencentConfig
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		if err := cfg.validateAuth(); err != nil {
			return err
		}
		_, err := tencentRequest(ctx, cfg, "DescribeCertificates", map[string]any{"Limit": 1})
		return err
	case CredentialAliyun:
		var cfg AliyunConfig
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		if err := cfg.validateAuth(); err != nil {
			return err
		}
		_, err := aliyunRPC(ctx, "https://cdn.aliyuncs.com/", "2018-05-10", "DescribeCdnService", cfg.AccessKeyID, cfg.AccessKeySecret, nil)
		return err
	case CredentialQiniu:
		var cfg QiniuConfig
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		if err := cfg.validateAuth(); err != nil {
			return err
		}
		_, err := qiniuRequest(ctx, cfg, http.MethodGet, "/sslcert", "", nil)
		return err
	case CredentialBaota:
		var cfg BaotaConfig
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		return TestBaota(ctx, cfg)
	case CredentialOnePanel:
		var cfg OnePanelConfig
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		return TestOnePanel(ctx, cfg)
	case CredentialSSH:
		var cfg SSHConfig
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		return TestSSHCredential(ctx, cfg)
	}
	return errors.New("unsupported credential type")
}

// resolveCredentialConfig loads a stored credential and merges its whitelisted
// fields into the target config. credentialID takes precedence over an ID
// embedded in the config JSON.
func (d *Deployer) resolveCredentialConfig(ctx context.Context, depType string, configJSON []byte, credentialID string) ([]byte, error) {
	if credentialID == "" {
		var ref struct {
			CredentialID string `json:"credential_id"`
		}
		_ = json.Unmarshal(configJSON, &ref)
		credentialID = strings.TrimSpace(ref.CredentialID)
	}
	if credentialID == "" {
		return configJSON, nil
	}

	credType, ok := CredentialTypeForDeployment(depType)
	if !ok {
		return nil, fmt.Errorf("deployment type %q does not support credentials", depType)
	}
	if d.Store == nil || d.Box == nil {
		return nil, errors.New("credential store is not configured")
	}
	cred, err := d.Store.GetDeploymentCredential(ctx, credentialID)
	if err != nil {
		return nil, fmt.Errorf("load credential: %w", err)
	}
	if cred.Type != credType {
		return nil, fmt.Errorf("credential type %q does not match deployment type %q", cred.Type, depType)
	}
	plain, err := d.Box.Decrypt(cred.ConfigEnc, CredentialAAD(cred.ID))
	if err != nil {
		return nil, fmt.Errorf("decrypt credential: %w", err)
	}

	var base map[string]any
	if err := json.Unmarshal(configJSON, &base); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	if base == nil {
		base = map[string]any{}
	}
	var auth map[string]any
	if err := json.Unmarshal(plain, &auth); err != nil {
		return nil, fmt.Errorf("invalid credential: %w", err)
	}
	fields, _ := CredentialFields(credType)
	for _, field := range fields {
		if value, ok := auth[field]; ok {
			base[field] = value
		}
	}
	return json.Marshal(base)
}
