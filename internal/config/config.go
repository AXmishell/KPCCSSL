// Package config loads runtime configuration from environment variables.
package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

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

	// Version is injected by the build.
	Version string
}

// Load reads configuration from the environment and ensures the data layout
// exists. It never generates secrets; that is the caller's responsibility.
func Load() (*Config, error) {
	dataDir := env("KPCC_DATA_DIR", "/data")
	c := &Config{
		Listen:        env("KPCC_LISTEN", ":8080"),
		DataDir:       dataDir,
		DBPath:        env("KPCC_DB_PATH", filepath.Join(dataDir, "kpccssl.db")),
		MasterKey:     os.Getenv("KPCC_MASTER_KEY"),
		MasterKeyFile: env("KPCC_MASTER_KEY_FILE", filepath.Join(dataDir, "master.key")),
		ACMEBin:       env("KPCC_ACME_BIN", "/opt/acme.sh/acme.sh"),
		ACMEHome:      env("KPCC_ACME_HOME", filepath.Join(dataDir, "acme")),
		CertDir:       env("KPCC_CERT_DIR", filepath.Join(dataDir, "certs")),
		LogDir:        env("KPCC_LOG_DIR", filepath.Join(dataDir, "logs")),
		BaseURL:       strings.TrimRight(os.Getenv("KPCC_BASE_URL"), "/"),
		SetupToken:    os.Getenv("KPCC_SETUP_TOKEN"),
		LogLevel:      env("KPCC_LOG_LEVEL", "info"),
		RetentionDays: envInt("KPCC_RETENTION_DAYS", 30),
		HTTPPort:      envInt("KPCC_HTTP_PORT", 8081),
		TLSPort:       envInt("KPCC_TLS_PORT", 8443),
		Dev:           envBool("KPCC_DEV", false),
		FakeACME:      envBool("KPCC_FAKE_ACME", false),
	}

	if raw := os.Getenv("KPCC_DEPLOY_ROOTS"); raw != "" {
		for _, p := range strings.Split(raw, string(os.PathListSeparator)) {
			if p = strings.TrimSpace(p); p != "" {
				c.DeployRoots = append(c.DeployRoots, p)
			}
		}
	}
	webroots := env("KPCC_WEBROOT_ROOTS", "/var/www"+string(os.PathListSeparator)+filepath.Join(dataDir, "webroot"))
	for _, p := range strings.Split(webroots, string(os.PathListSeparator)) {
		if p = strings.TrimSpace(p); p != "" {
			c.WebrootRoots = append(c.WebrootRoots, p)
		}
	}

	conc, err := strconv.Atoi(env("KPCC_JOB_CONCURRENCY", "1"))
	if err != nil || conc < 1 {
		return nil, fmt.Errorf("KPCC_JOB_CONCURRENCY must be a positive integer")
	}
	c.JobConcurrency = conc

	timeout, err := time.ParseDuration(env("KPCC_JOB_TIMEOUT", "30m"))
	if err != nil || timeout <= 0 {
		return nil, fmt.Errorf("KPCC_JOB_TIMEOUT must be a positive duration")
	}
	c.JobTimeout = timeout

	if raw := os.Getenv("KPCC_TRUSTED_PROXIES"); raw != "" {
		for _, part := range strings.Split(raw, ",") {
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
				return nil, fmt.Errorf("invalid KPCC_TRUSTED_PROXIES entry %q", part)
			}
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			c.TrustedProxies = append(c.TrustedProxies, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
		}
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

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	v, err := strconv.Atoi(os.Getenv(key))
	if err != nil {
		return def
	}
	return v
}

func envBool(key string, def bool) bool {
	switch strings.ToLower(os.Getenv(key)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}
