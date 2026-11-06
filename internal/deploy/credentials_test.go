package deploy

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/axmishell/kpccssl/internal/secret"
	"github.com/axmishell/kpccssl/internal/store"
)

func TestCredentialTypeMapping(t *testing.T) {
	cases := map[string]string{
		"tencent_cdn": "tencent", "tencent_waf": "tencent", "tencent_cos": "tencent", "tencent_teo": "tencent",
		"aliyun_cdn": "aliyun", "aliyun_dcdn": "aliyun", "aliyun_oss": "aliyun", "aliyun_waf": "aliyun",
		"qiniu_cdn":  "qiniu",
		"baota_site": "baota", "1panel_panel": "1panel", "ssh": "ssh",
	}
	for depType, want := range cases {
		got, ok := CredentialTypeForDeployment(depType)
		if !ok || got != want {
			t.Fatalf("%s -> %q, %v", depType, got, ok)
		}
	}
	for _, depType := range []string{"local_file", "reload", "webhook"} {
		if _, ok := CredentialTypeForDeployment(depType); ok {
			t.Fatalf("%s unexpectedly requires credentials", depType)
		}
	}
}

func TestValidateCredential(t *testing.T) {
	valid := map[string]string{
		"tencent": `{"secret_id":"id","secret_key":"key"}`,
		"aliyun":  `{"access_key_id":"id","access_key_secret":"secret"}`,
		"qiniu":   `{"access_key":"ak","access_secret":"sk"}`,
		"baota":   `{"url":"https://panel.example.com","api_key":"key"}`,
		"1panel":  `{"url":"https://panel.example.com","api_key":"key","version":"v2"}`,
		"ssh":     `{"host":"example.com","user":"root","private_key":"KEY","host_key_sha256":"SHA256:abc"}`,
	}
	for credType, config := range valid {
		if err := ValidateCredential(credType, config); err != nil {
			t.Fatalf("%s rejected: %v", credType, err)
		}
	}
	for credType, config := range map[string]string{
		"tencent": `{"secret_id":"id"}`,
		"aliyun":  `{}`,
		"qiniu":   `{"access_key":"ak"}`,
		"baota":   `{"url":"https://panel.example.com"}`,
		"1panel":  `{"url":"https://panel.example.com","api_key":"key","version":"v9"}`,
		"ssh":     `{"host":"example.com","user":"root"}`,
		"bogus":   `{}`,
	} {
		if err := ValidateCredential(credType, config); err == nil {
			t.Fatalf("%s accepted invalid config", credType)
		}
	}
}

func TestResolveCredentialConfig(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	box, err := secret.LoadOrCreate(filepath.Join(t.TempDir(), "master.key"), "")
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte(`{"secret_id":"id","secret_key":"key","ignored":"value"}`)
	enc, err := box.Encrypt(plain, CredentialAAD("c1"))
	if err != nil {
		t.Fatal(err)
	}
	now := store.Now()
	if err := st.CreateDeploymentCredential(ctx, &store.DeploymentCredential{
		ID: "c1", Name: "Tencent", Type: "tencent", ConfigEnc: enc, KeyVersion: 1,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	d := New(st, box, nil)
	config := []byte(`{"credential_id":"c1","domain":"a.example.com"}`)
	resolved, err := d.resolveCredentialConfig(ctx, "tencent_cdn", config, "")
	if err != nil {
		t.Fatal(err)
	}
	var cfg TencentConfig
	if err := json.Unmarshal(resolved, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.SecretID != "id" || cfg.SecretKey != "key" || cfg.Domain != "a.example.com" {
		t.Fatalf("resolved config = %#v", cfg)
	}
	var raw map[string]any
	_ = json.Unmarshal(resolved, &raw)
	if _, ok := raw["ignored"]; ok {
		t.Fatal("credential field outside the whitelist was merged")
	}

	if err := d.Validate("tencent_cdn", string(config)); err != nil {
		t.Fatalf("validate with credential failed: %v", err)
	}
	if _, err := d.resolveCredentialConfig(ctx, "aliyun_cdn", config, ""); err == nil {
		t.Fatal("credential type mismatch accepted")
	}
	if _, err := d.resolveCredentialConfig(ctx, "tencent_cdn", config, "missing"); err == nil {
		t.Fatal("missing credential accepted")
	}
}

func TestSSHConfigValidateAuth(t *testing.T) {
	base := SSHConfig{Host: "example.com", User: "root", PrivateKey: "KEY", HostKeySHA256: "SHA256:abc"}
	if err := base.validateAuth(); err != nil {
		t.Fatalf("valid auth rejected: %v", err)
	}
	// Directory is a target property, not a credential property.
	if err := base.validate(); err == nil {
		t.Fatal("target validation accepted a credential-only config")
	}
}
