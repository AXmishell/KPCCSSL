// Package config loads runtime configuration from a YAML file and environment
// variables. Environment variables always win over file values, which win over
// built-in defaults.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultFile is the config file discovered in the working directory when
// KPCC_CONFIG is not set.
const DefaultFile = "kpccssl.yaml"

// Config is the fully resolved runtime configuration.
type Config struct {
	Listen         string
	DataDir        string
	DBPath         string
	MasterKey      string
	MasterKeyFile  string
	ACMEBin        string
	ACMEHome       string
	CertDir        string
	LogDir         string
	BaseURL        string
	SetupToken     string
	LogLevel       string
	TrustedProxies []*net.IPNet

	JobConcurrency int
	JobTimeout     time.Duration
	RetentionDays  int
	HTTPPort       int
	TLSPort        int

	// Dev disables Secure cookies and relaxes the __Host- prefix so the SPA
	// can be used over plain HTTP on localhost.
	Dev bool

	// FakeACME swaps the acme.sh executor for the built-in fake engine. It is
	// meant for local UI development and E2E tests only.
	FakeACME bool

	// DeployRoots is the allow-list for local_file deployments.
	DeployRoots []string
	// WebrootRoots is the allow-list for HTTP-01 webroot directories.
	WebrootRoots []string

	// ConfigFile is the YAML file that was loaded, empty when none was used.
	ConfigFile string

	// Version is injected by the build.
	Version string
}

// fileConfig mirrors Config for YAML decoding. Pointer fields distinguish
// "not present" from a meaningful zero value.
type fileConfig struct {
	Listen         *string  `yaml:"listen"`
	DataDir        *string  `yaml:"data_dir"`
	DBPath         *string  `yaml:"db_path"`
	MasterKey      *string  `yaml:"master_key"`
	MasterKeyFile  *string  `yaml:"master_key_file"`
	ACMEBin        *string  `yaml:"acme_bin"`
	ACMEHome       *string  `yaml:"acme_home"`
	CertDir        *string  `yaml:"cert_dir"`
	LogDir         *string  `yaml:"log_dir"`
	BaseURL        *string  `yaml:"base_url"`
	SetupToken     *string  `yaml:"setup_token"`
	LogLevel       *string  `yaml:"log_level"`
	TrustedProxies []string `yaml:"trusted_proxies"`
	JobConcurrency *int     `yaml:"job_concurrency"`
	JobTimeout     *string  `yaml:"job_timeout"`
	RetentionDays  *int     `yaml:"retention_days"`
	HTTPPort       *int     `yaml:"http_port"`
	TLSPort        *int     `yaml:"tls_port"`
	Dev            *bool    `yaml:"dev"`
	FakeACME       *bool    `yaml:"fake_acme"`
	DeployRoots    []string `yaml:"deploy_roots"`
	WebrootRoots   []string `yaml:"webroot_roots"`
}

// Load reads configuration from $KPCC_CONFIG, or from kpccssl.yaml in the
// working directory when it exists, and overlays environment variables.
func Load() (*Config, error) {
	path := os.Getenv("KPCC_CONFIG")
	explicit := path != ""
	if path == "" {
		for _, candidate := range []string{DefaultFile, "kpccssl.yml"} {
			if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
				path = candidate
				break
			}
		}
	}
	return load(path, explicit)
}

// LoadFile reads configuration from an explicit YAML file and overlays
// environment variables. A missing file is an error.
func LoadFile(path string) (*Config, error) {
	return load(path, true)
}

func load(path string, mustExist bool) (*Config, error) {
	var f fileConfig
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			if mustExist {
				return nil, fmt.Errorf("read config %s: %w", path, err)
			}
			path = ""
		} else {
			dec := yaml.NewDecoder(bytes.NewReader(data))
			dec.KnownFields(true)
			if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
				return nil, fmt.Errorf("parse config %s: %w", path, err)
			}
		}
	}

	// DataDir is resolved first because the other paths default relative to it.
	dataDir := value("KPCC_DATA_DIR", f.DataDir, "/data")

	c := &Config{
		Listen:        value("KPCC_LISTEN", f.Listen, ":8080"),
		DataDir:       dataDir,
		DBPath:        value("KPCC_DB_PATH", f.DBPath, filepath.Join(dataDir, "kpccssl.db")),
		MasterKey:     value("KPCC_MASTER_KEY", f.MasterKey, ""),
		MasterKeyFile: value("KPCC_MASTER_KEY_FILE", f.MasterKeyFile, filepath.Join(dataDir, "master.key")),
		ACMEBin:       value("KPCC_ACME_BIN", f.ACMEBin, "/opt/acme.sh/acme.sh"),
		ACMEHome:      value("KPCC_ACME_HOME", f.ACMEHome, filepath.Join(dataDir, "acme")),
		CertDir:       value("KPCC_CERT_DIR", f.CertDir, filepath.Join(dataDir, "certs")),
		LogDir:        value("KPCC_LOG_DIR", f.LogDir, filepath.Join(dataDir, "logs")),
		BaseURL:       strings.TrimRight(value("KPCC_BASE_URL", f.BaseURL, ""), "/"),
		SetupToken:    value("KPCC_SETUP_TOKEN", f.SetupToken, ""),
		LogLevel:      value("KPCC_LOG_LEVEL", f.LogLevel, "info"),
		RetentionDays: intValue("KPCC_RETENTION_DAYS", f.RetentionDays, 30),
		HTTPPort:      intValue("KPCC_HTTP_PORT", f.HTTPPort, 8081),
		TLSPort:       intValue("KPCC_TLS_PORT", f.TLSPort, 8443),
		Dev:           boolValue("KPCC_DEV", f.Dev, false),
		FakeACME:      boolValue("KPCC_FAKE_ACME", f.FakeACME, false),
		ConfigFile:    path,
	}

	concurrency := intValue("KPCC_JOB_CONCURRENCY", f.JobConcurrency, 1)
	if concurrency < 1 {
		return nil, fmt.Errorf("job_concurrency must be a positive integer")
	}
	c.JobConcurrency = concurrency

	timeoutRaw := value("KPCC_JOB_TIMEOUT", f.JobTimeout, "30m")
	timeout, err := time.ParseDuration(timeoutRaw)
	if err != nil || timeout <= 0 {
		return nil, fmt.Errorf("job_timeout must be a positive duration, got %q", timeoutRaw)
	}
	c.JobTimeout = timeout

	proxies := f.TrustedProxies
	if raw, ok := os.LookupEnv("KPCC_TRUSTED_PROXIES"); ok && raw != "" {
		proxies = strings.Split(raw, ",")
	}
	for _, part := range proxies {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, n, err := net.ParseCIDR(part); err == nil {
			c.TrustedProxies = append(c.TrustedProxies, n)
			continue
		}
		ip := net.ParseIP(part)
		if ip == nil {
			return nil, fmt.Errorf("invalid trusted proxy entry %q", part)
		}
		bits := 32
		if ip.To4() == nil {
			bits = 128
		}
		c.TrustedProxies = append(c.TrustedProxies, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
	}

	c.DeployRoots = pathList("KPCC_DEPLOY_ROOTS", f.DeployRoots)
	c.WebrootRoots = pathList("KPCC_WEBROOT_ROOTS", f.WebrootRoots)
	if c.WebrootRoots == nil {
		c.WebrootRoots = splitPaths("/var/www" + string(os.PathListSeparator) + filepath.Join(dataDir, "webroot"))
	}

	return c, c.EnsureDirs()
}

// EnsureDirs creates the persistent data layout with restrictive permissions.
func (c *Config) EnsureDirs() error {
	dirs := []string{c.DataDir, c.ACMEHome, c.CertDir, c.LogDir, filepath.Join(c.LogDir, "jobs"), filepath.Join(c.DataDir, "tmp", "jobs"), filepath.Join(c.DataDir, "deploy"),
		filepath.Join(c.DataDir, "webroot")}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", d, err)
		}
	}
	return nil
}

// Secure reports whether cookies must carry the Secure attribute.
func (c *Config) Secure() bool {
	return !c.Dev
}

// SessionCookieName returns the cookie name. Production uses the __Host-
// prefix, which browsers only accept together with Secure + Path=/.
func (c *Config) SessionCookieName() string {
	if c.Dev {
		return "kpccssl_session"
	}
	return "__Host-kpccssl_session"
}

// IsTrustedProxy reports whether the remote address belongs to a configured
// reverse proxy network.
func (c *Config) IsTrustedProxy(remoteIP string) bool {
	ip := net.ParseIP(remoteIP)
	if ip == nil {
		return false
	}
	for _, n := range c.TrustedProxies {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// value resolves one string setting: environment, then file, then default.
func value(envKey string, file *string, def string) string {
	if v, ok := os.LookupEnv(envKey); ok && v != "" {
		return v
	}
	if file != nil {
		return *file
	}
	return def
}

// intValue resolves one integer setting: environment, then file, then default.
func intValue(envKey string, file *int, def int) int {
	if v, ok := os.LookupEnv(envKey); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
		return def
	}
	if file != nil {
		return *file
	}
	return def
}

// boolValue resolves one boolean setting: environment, then file, then default.
func boolValue(envKey string, file *bool, def bool) bool {
	if v, ok := os.LookupEnv(envKey); ok && v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
		return def
	}
	if file != nil {
		return *file
	}
	return def
}

// pathList resolves a list setting. The environment variable uses the OS path
// list separator; the YAML value is already a list.
func pathList(envKey string, file []string) []string {
	if raw, ok := os.LookupEnv(envKey); ok && raw != "" {
		return splitPaths(raw)
	}
	if file != nil {
		return file
	}
	return nil
}

func splitPaths(raw string) []string {
	var out []string
	for _, p := range strings.Split(raw, string(os.PathListSeparator)) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
