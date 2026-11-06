// Package deploy implements structured deployment targets. Arbitrary shell
// hooks are intentionally impossible: reload commands are fixed argv templates.
package deploy

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/axmishell/kpccssl/internal/id"
	"github.com/axmishell/kpccssl/internal/secret"
	"github.com/axmishell/kpccssl/internal/store"
)

func newID() string { return id.New() }

// LocalFileConfig writes certificate material into an allow-listed directory.
type LocalFileConfig struct {
	Directory     string `json:"directory"`
	CertFile      string `json:"cert_file"`
	KeyFile       string `json:"key_file"`
	ChainFile     string `json:"chain_file"`
	FullchainFile string `json:"fullchain_file"`
	Mode          string `json:"mode"`
	Backup        bool   `json:"backup"`
}

// ReloadConfig selects one of the fixed reload templates.
type ReloadConfig struct {
	Target string `json:"target"` // nginx | systemd-nginx | systemd-apache | apache
}

// WebhookConfig posts signed JSON to a public HTTPS endpoint.
type WebhookConfig struct {
	URL    string `json:"url"`
	Secret string `json:"secret"`
}

// Deployer executes deployment targets.
type Deployer struct {
	Store     *store.Store
	Box       *secret.Box
	AllowDirs []string
	Client    *http.Client
}

// SafeTransport is an HTTP transport that rejects private/loopback targets at
// dial time. Exported so notification webhooks use the same DNS-rebinding
// protection as certificate deployments.
func SafeTransport() *http.Transport {
	return &http.Transport{
		DialContext:  safeDialer(10 * time.Second),
		MaxIdleConns: 10,
	}
}

// New creates a deployer. allowDirs are absolute roots permitted for local
// file deployment.
func New(st *store.Store, box *secret.Box, allowDirs []string) *Deployer {
	return &Deployer{
		Store:     st,
		Box:       box,
		AllowDirs: allowDirs,
		Client: &http.Client{
			Timeout:   20 * time.Second,
			Transport: SafeTransport(),
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 3 {
					return errors.New("too many redirects")
				}
				if req.URL.Scheme != "https" {
					return errors.New("redirect to non-HTTPS URL refused")
				}
				return nil
			},
		},
	}
}

// Run deploys a revision to a target and records the run.
func (d *Deployer) Run(ctx context.Context, dep *store.Deployment, rev *store.Revision) (err error) {
	run := &store.DeploymentRun{
		ID:           newID(),
		DeploymentID: dep.ID,
		RevisionID:   rev.ID,
		Status:       "running",
		CreatedAt:    store.Now(),
	}
	_ = d.Store.CreateDeploymentRun(ctx, run)

	defer func() {
		status := "succeeded"
		var msg *string
		if err != nil {
			status = "failed"
			m := truncate(err.Error(), 1000)
			msg = &m
		}
		// Use a fresh context: the job context may be canceled on shutdown or
		// timeout, but the run outcome must still be recorded.
		finishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = d.Store.FinishDeploymentRun(finishCtx, run.ID, status, msg)
		_ = d.Store.SetDeploymentLastRun(finishCtx, dep.ID, status, store.Now())
	}()

	plain, err := d.Box.Decrypt(dep.ConfigEnc, aad(dep.ID, "config"))
	if err != nil {
		return fmt.Errorf("decrypt deployment config: %w", err)
	}
	switch dep.Type {
	case "local_file":
		var cfg LocalFileConfig
		if err := json.Unmarshal(plain, &cfg); err != nil {
			return fmt.Errorf("invalid config: %w", err)
		}
		return d.localFile(cfg, rev)
	case "reload":
		var cfg ReloadConfig
		if err := json.Unmarshal(plain, &cfg); err != nil {
			return fmt.Errorf("invalid config: %w", err)
		}
		return runReload(ctx, cfg.Target)
	case "webhook":
		var cfg WebhookConfig
		if err := json.Unmarshal(plain, &cfg); err != nil {
			return fmt.Errorf("invalid config: %w", err)
		}
		return d.webhook(ctx, cfg, dep, rev)
	}
	return fmt.Errorf("unsupported deployment type %q", dep.Type)
}

// Validate checks a deployment config before it is stored.
func (d *Deployer) Validate(depType, configJSON string) error {
	switch depType {
	case "local_file":
		var cfg LocalFileConfig
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		if cfg.Directory == "" {
			return errors.New("directory is required")
		}
		_, err := d.safeDir(cfg.Directory)
		return err
	case "reload":
		var cfg ReloadConfig
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		if _, ok := reloadCommands[cfg.Target]; !ok {
			return errors.New("unsupported reload target")
		}
		return nil
	case "webhook":
		var cfg WebhookConfig
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		return ValidateWebhookURL(cfg.URL)
	}
	return errors.New("unsupported deployment type")
}

func (d *Deployer) localFile(cfg LocalFileConfig, rev *store.Revision) error {
	dir, err := d.safeDir(cfg.Directory)
	if err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if cfg.Mode != "" {
		v, err := parseMode(cfg.Mode)
		if err != nil {
			return err
		}
		mode = v
	}
	files := []struct {
		dst    string
		src    string
		secret bool
	}{
		{cfg.CertFile, rev.CertPath, false},
		{cfg.KeyFile, rev.KeyPath, true},
		{cfg.ChainFile, rev.ChainPath, false},
		{cfg.FullchainFile, rev.FullchainPath, false},
	}
	for _, f := range files {
		if f.dst == "" {
			continue
		}
		name, err := safeJoin(dir, f.dst)
		if err != nil {
			return err
		}
		fileMode := mode
		if f.secret {
			// Private keys are never group/world readable, regardless of the
			// configured certificate mode.
			fileMode = 0o600
		}
		if err := atomicCopy(f.src, name, fileMode, cfg.Backup); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	return nil
}

func (d *Deployer) safeDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("directory must exist: %w", err)
	}
	for _, root := range d.AllowDirs {
		r, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		if real, err := filepath.EvalSymlinks(r); err == nil {
			r = real
		}
		rel, err := filepath.Rel(r, resolved)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("directory %s is not inside an allowed root", dir)
}

var reloadCommands = map[string][]string{
	"nginx":          {"nginx", "-s", "reload"},
	"systemd-nginx":  {"systemctl", "reload", "nginx"},
	"systemd-apache": {"systemctl", "reload", "apache2"},
	"apache":         {"apachectl", "graceful"},
}

func runReload(ctx context.Context, target string) error {
	argv, ok := reloadCommands[target]
	if !ok {
		return errors.New("unsupported reload target")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("reload failed: %s", truncate(string(out), 500))
	}
	return nil
}

func (d *Deployer) webhook(ctx context.Context, cfg WebhookConfig, dep *store.Deployment, rev *store.Revision) error {
	if err := ValidateWebhookURL(cfg.URL); err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]any{
		"event":          "certificate.deployed",
		"deployment_id":  dep.ID,
		"certificate_id": dep.CertificateID,
		"revision_id":    rev.ID,
		"not_after":      rev.NotAfter,
		"fingerprint":    rev.FingerprintSHA256,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.Secret != "" {
		req.Header.Set("X-KPCCSSL-Signature", sign(cfg.Secret, body))
	}
	resp, err := d.Client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook request failed: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// ValidateWebhookURL requires HTTPS and a host that is not private, loopback
// or link-local.
func ValidateWebhookURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("invalid URL")
	}
	if u.Scheme != "https" {
		return errors.New("webhook URL must use https")
	}
	if u.Hostname() == "" {
		return errors.New("webhook URL must include a host")
	}
	ips, err := net.LookupIP(u.Hostname())
	if err != nil {
		return fmt.Errorf("cannot resolve webhook host: %w", err)
	}
	for _, ip := range ips {
		if isPrivate(ip) {
			return errors.New("webhook host resolves to a private address")
		}
	}
	return nil
}

func safeDialer(timeout time.Duration) func(ctx context.Context, network, addr string) (net.Conn, error) {
	d := &net.Dialer{Timeout: timeout}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := net.LookupIP(host)
		if err != nil {
			return nil, err
		}
		var lastErr error
		for _, ip := range ips {
			if isPrivate(ip) {
				return nil, errors.New("refusing to connect to private address")
			}
			conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
}

func isPrivate(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast()
}

func atomicCopy(src, dst string, mode os.FileMode, backup bool) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if backup {
		if _, err := os.Stat(dst); err == nil {
			data, err := os.ReadFile(dst)
			if err != nil {
				return err
			}
			if err := os.WriteFile(dst+".bak", data, mode); err != nil { // #nosec G703 -- dst is validated by safeJoin/safeDir
				return err
			}
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".kpccssl-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		if backup {
			if data, rerr := os.ReadFile(dst + ".bak"); rerr == nil {
				_ = os.WriteFile(dst, data, mode) // #nosec G703 -- dst is validated by safeJoin/safeDir
			}
		}
		return err
	}
	return nil
}

func safeJoin(root, name string) (string, error) {
	if filepath.IsAbs(name) {
		return "", errors.New("file name must be relative")
	}
	clean := filepath.Clean(name)
	if clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return "", errors.New("file name escapes directory")
	}
	dst := filepath.Join(root, clean)
	// Resolve the parent directory, not just the configured root. Without this
	// a symlinked subdirectory inside the allow-list can redirect the atomic
	// write to an arbitrary path outside it.
	parent, err := filepath.EvalSymlinks(filepath.Dir(dst))
	if err != nil {
		return "", fmt.Errorf("destination directory must exist: %w", err)
	}
	info, err := os.Stat(parent)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("destination directory is not a directory")
	}
	if !withinDir(root, parent) {
		return "", errors.New("file destination escapes directory")
	}
	resolved := filepath.Join(parent, filepath.Base(dst))
	if link, err := os.Lstat(resolved); err == nil && link.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("file destination must not be a symlink")
	}
	return resolved, nil
}

func withinDir(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func parseMode(s string) (os.FileMode, error) {
	var v uint32
	if _, err := fmt.Sscanf(s, "%o", &v); err != nil {
		return 0, errors.New("mode must be octal, e.g. 0640")
	}
	return os.FileMode(v), nil
}

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func aad(id, field string) string { return "deployments:" + id + ":" + field }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
