package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// clearEnv removes KPCC_* variables for the duration of the test. Setting a
// variable to the empty string is equivalent to unsetting it for Load.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		key := strings.SplitN(kv, "=", 2)[0]
		if strings.HasPrefix(key, "KPCC_") {
			t.Setenv(key, "")
		}
	}
}

func writeConfig(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadFileValues(t *testing.T) {
	clearEnv(t)
	dataDir := t.TempDir()
	path := writeConfig(t, t.TempDir(), "kpccssl.yaml", `
listen: "127.0.0.1:9090"
data_dir: `+dataDir+`
base_url: "https://certs.example.com/"
setup_token: "fixed-token"
log_level: debug
job_concurrency: 2
job_timeout: 5m
retention_days: 7
http_port: 9081
tls_port: 9443
dev: true
fake_acme: true
trusted_proxies:
  - 10.0.0.0/8
  - 192.168.1.1
deploy_roots:
  - /etc/nginx/certs
  - /srv/certs
webroot_roots:
  - /var/www/html
`)

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"listen", cfg.Listen, "127.0.0.1:9090"},
		{"data_dir", cfg.DataDir, dataDir},
		{"db_path", cfg.DBPath, filepath.Join(dataDir, "kpccssl.db")},
		{"master_key_file", cfg.MasterKeyFile, filepath.Join(dataDir, "master.key")},
		{"acme_home", cfg.ACMEHome, filepath.Join(dataDir, "acme")},
		{"cert_dir", cfg.CertDir, filepath.Join(dataDir, "certs")},
		{"log_dir", cfg.LogDir, filepath.Join(dataDir, "logs")},
		{"base_url", cfg.BaseURL, "https://certs.example.com"},
		{"setup_token", cfg.SetupToken, "fixed-token"},
		{"log_level", cfg.LogLevel, "debug"},
		{"job_concurrency", cfg.JobConcurrency, 2},
		{"job_timeout", cfg.JobTimeout, 5 * time.Minute},
		{"retention_days", cfg.RetentionDays, 7},
		{"http_port", cfg.HTTPPort, 9081},
		{"tls_port", cfg.TLSPort, 9443},
		{"dev", cfg.Dev, true},
		{"fake_acme", cfg.FakeACME, true},
		{"config_file", cfg.ConfigFile, path},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}

	if len(cfg.TrustedProxies) != 2 {
		t.Fatalf("trusted proxies = %v, want 2 entries", cfg.TrustedProxies)
	}
	if cfg.TrustedProxies[1].String() != "192.168.1.1/32" {
		t.Errorf("bare proxy IP = %s, want /32", cfg.TrustedProxies[1])
	}
	if got := strings.Join(cfg.DeployRoots, ","); got != "/etc/nginx/certs,/srv/certs" {
		t.Errorf("deploy roots = %q", got)
	}
	if got := strings.Join(cfg.WebrootRoots, ","); got != "/var/www/html" {
		t.Errorf("webroot roots = %q", got)
	}
	for _, dir := range []string{dataDir, cfg.ACMEHome, cfg.CertDir, cfg.LogDir, filepath.Join(cfg.LogDir, "jobs")} {
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			t.Errorf("directory %s not created: %v", dir, err)
		}
	}
}

func TestEnvOverridesFile(t *testing.T) {
	clearEnv(t)
	fileDataDir := t.TempDir()
	envDataDir := t.TempDir()
	path := writeConfig(t, t.TempDir(), "kpccssl.yaml", `
listen: ":1111"
data_dir: `+fileDataDir+`
log_level: warn
job_concurrency: 4
deploy_roots:
  - /from/file
`)

	t.Setenv("KPCC_LISTEN", ":2222")
	t.Setenv("KPCC_DATA_DIR", envDataDir)
	t.Setenv("KPCC_LOG_LEVEL", "error")
	t.Setenv("KPCC_DEPLOY_ROOTS", "/from/env"+string(os.PathListSeparator)+"/second")

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if cfg.Listen != ":2222" {
		t.Errorf("listen = %q, want env value", cfg.Listen)
	}
	if cfg.DataDir != envDataDir {
		t.Errorf("data_dir = %q, want env value", cfg.DataDir)
	}
	if cfg.DBPath != filepath.Join(envDataDir, "kpccssl.db") {
		t.Errorf("db_path = %q, want derived from env data_dir", cfg.DBPath)
	}
	if cfg.LogLevel != "error" {
		t.Errorf("log_level = %q, want env value", cfg.LogLevel)
	}
	if cfg.JobConcurrency != 4 {
		t.Errorf("job_concurrency = %d, want file value", cfg.JobConcurrency)
	}
	if got := strings.Join(cfg.DeployRoots, ","); got != "/from/env,/second" {
		t.Errorf("deploy roots = %q", got)
	}
}

func TestLoadDiscoversDefaultFile(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	t.Chdir(dir)
	dataDir := t.TempDir()
	writeConfig(t, dir, DefaultFile, "data_dir: "+dataDir+"\nlisten: \":3333\"\n")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ConfigFile != DefaultFile {
		t.Errorf("config file = %q, want %q", cfg.ConfigFile, DefaultFile)
	}
	if cfg.Listen != ":3333" {
		t.Errorf("listen = %q", cfg.Listen)
	}
}

func TestLoadEnvConfigPath(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	path := writeConfig(t, dir, "custom.yaml", "data_dir: "+t.TempDir()+"\n")
	t.Setenv("KPCC_CONFIG", path)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ConfigFile != path {
		t.Errorf("config file = %q, want %q", cfg.ConfigFile, path)
	}
}

func TestLoadFileErrors(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()

	cases := []struct {
		name    string
		content string
	}{
		{"unknown field", "data_dir: " + t.TempDir() + "\nunknown_key: 1\n"},
		{"invalid duration", "data_dir: " + t.TempDir() + "\njob_timeout: soon\n"},
		{"invalid concurrency", "data_dir: " + t.TempDir() + "\njob_concurrency: 0\n"},
		{"invalid proxy", "data_dir: " + t.TempDir() + "\ntrusted_proxies:\n  - not-an-ip\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := writeConfig(t, dir, "bad.yaml", c.content)
			if _, err := LoadFile(path); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestLoadFileMissing(t *testing.T) {
	clearEnv(t)
	missing := filepath.Join(t.TempDir(), "nope.yaml")
	if _, err := LoadFile(missing); err == nil {
		t.Fatal("LoadFile must fail for a missing explicit file")
	}
}

func TestEmptyFileUsesDefaults(t *testing.T) {
	clearEnv(t)
	dataDir := t.TempDir()
	t.Setenv("KPCC_DATA_DIR", dataDir)
	path := writeConfig(t, t.TempDir(), "empty.yaml", "")

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if cfg.Listen != ":8080" || cfg.JobConcurrency != 1 || cfg.JobTimeout != 30*time.Minute {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
}

// The committed example must stay loadable and free of unknown fields.
func TestExampleFileParses(t *testing.T) {
	clearEnv(t)
	t.Setenv("KPCC_DATA_DIR", t.TempDir())
	if _, err := LoadFile(filepath.Join("..", "..", "kpccssl.example.yaml")); err != nil {
		t.Fatalf("example config: %v", err)
	}
}
