// Package acmesh executes the pinned acme.sh CLI. It is the only package
// allowed to create external processes.
package acmesh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/axmishell/kpccssl/internal/acme"
)

// ErrSkipped is returned when acme.sh exits 2 during renewal: the certificate
// is not due yet (or the CA said so). It is not a failure.
var ErrSkipped = errors.New("renewal skipped: certificate not due")

// Config pins the CLI and its state directory.
type Config struct {
	Bin  string
	Home string
}

// Executor runs acme.sh serially. A global flock protects the shared home;
// per-certificate locks are layered on by the job service.
type Executor struct {
	cfg Config
	mu  sync.Mutex
}

// New returns an executor for the given binary and home.
func New(bin, home string) *Executor {
	return &Executor{cfg: Config{Bin: bin, Home: home}}
}

// Home exposes the acme home path.
func (e *Executor) Home() string { return e.cfg.Home }

// Version runs `acme.sh --version`.
func (e *Executor) Version(ctx context.Context) (string, error) {
	var buf bytes.Buffer
	if _, err := e.run(ctx, VersionArgs(), nil, &buf, false); err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line, nil
		}
	}
	return "", nil
}

// RegisterAccount registers or updates an ACME account.
func (e *Executor) RegisterAccount(ctx context.Context, req acme.RegisterRequest, log io.Writer) error {
	args, err := RegisterArgs(req, e.cfg.Home)
	if err != nil {
		return err
	}
	_, err = e.run(ctx, args, nil, log, false)
	return err
}

// Issue requests a certificate and exports it into stageDir.
func (e *Executor) Issue(ctx context.Context, req acme.IssueRequest, stageDir string, log io.Writer) (acme.ExportPaths, error) {
	paths := exportPaths(stageDir)
	args, err := IssueArgs(req, e.cfg.Home)
	if err != nil {
		return paths, err
	}
	if _, err := e.run(ctx, args, req.Env, log, false); err != nil {
		return paths, err
	}
	installArgs, err := InstallArgs(req.Domains[0], req.KeyType, e.cfg.Home, paths)
	if err != nil {
		return paths, err
	}
	if _, err := e.run(ctx, installArgs, req.Env, log, false); err != nil {
		return paths, err
	}
	return paths, verifyExport(paths)
}

// Renew renews an existing certificate and exports it into stageDir.
func (e *Executor) Renew(ctx context.Context, req acme.RenewRequest, stageDir string, log io.Writer) (acme.ExportPaths, error) {
	paths := exportPaths(stageDir)
	args, err := RenewArgs(req, e.cfg.Home)
	if err != nil {
		return paths, err
	}
	if _, err := e.run(ctx, args, req.Env, log, true); err != nil {
		if errors.Is(err, ErrSkipped) {
			return paths, ErrSkipped
		}
		return paths, err
	}
	installArgs, err := InstallArgs(req.Domain, req.KeyType, e.cfg.Home, paths)
	if err != nil {
		return paths, err
	}
	if _, err := e.run(ctx, installArgs, req.Env, log, false); err != nil {
		return paths, err
	}
	return paths, verifyExport(paths)
}

// Revoke revokes a certificate.
func (e *Executor) Revoke(ctx context.Context, req acme.RevokeRequest, log io.Writer) error {
	args, err := RevokeArgs(req, e.cfg.Home)
	if err != nil {
		return err
	}
	_, err = e.run(ctx, args, req.Env, log, false)
	return err
}

// List parses `--list --listraw`.
func (e *Executor) List(ctx context.Context, log io.Writer) ([]acme.ListEntry, error) {
	var buf bytes.Buffer
	out := io.MultiWriter(log, &buf)
	_, err := e.run(ctx, ListArgs(e.cfg.Home), nil, out, false)
	if err != nil {
		if strings.Contains(strings.ToLower(buf.String()), "nothing to list") {
			return nil, nil
		}
		return nil, err
	}
	return ParseListRaw(buf.String()), nil
}

// run executes the binary with a minimal environment, serialising on the acme
// home lock. The returned output is the tail used for error classification.
func (e *Executor) run(ctx context.Context, args []string, extraEnv map[string]string, log io.Writer, allowSkip bool) (string, error) {
	if e.cfg.Bin == "" {
		return "", fmt.Errorf("acme.sh binary is not configured")
	}
	if _, err := os.Stat(e.cfg.Bin); err != nil {
		return "", fmt.Errorf("acme.sh binary %s: %w", e.cfg.Bin, err)
	}
	if err := os.MkdirAll(e.cfg.Home, 0o700); err != nil {
		return "", err
	}
	unlock, err := e.lock(ctx)
	if err != nil {
		return "", err
	}
	defer unlock()

	e.mu.Lock()
	defer e.mu.Unlock()

	cmd := exec.CommandContext(ctx, e.cfg.Bin, args...)
	cmd.Env = buildEnv(e.cfg.Home, extraEnv)
	tail := &tailBuffer{max: 64 * 1024}
	if log != nil {
		cmd.Stdout = io.MultiWriter(log, tail)
		cmd.Stderr = io.MultiWriter(log, tail)
	} else {
		cmd.Stdout, cmd.Stderr = tail, tail
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 10 * time.Second

	err = cmd.Run()
	output := tail.String()
	if ctx.Err() != nil {
		return output, &acme.Error{Code: acme.CodeTimeout, Message: "job timed out", Output: output}
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if exitErr.ExitCode() == 2 && allowSkip {
				return output, ErrSkipped
			}
			classified := acme.Classify(output, exitErr.ExitCode())
			if classified == nil {
				classified = &acme.Error{Code: acme.CodeUnknown, Message: err.Error(), ExitCode: exitErr.ExitCode(), Output: output}
			}
			return output, classified
		}
		return output, &acme.Error{Code: acme.CodeNetwork, Message: err.Error(), Output: output}
	}
	return output, nil
}

// lock acquires an exclusive flock on <home>/.lock, polling until ctx ends.
func (e *Executor) lock(ctx context.Context) (func(), error) {
	path := filepath.Join(e.cfg.Home, ".lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open acme lock: %w", err)
	}
	for {
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

func buildEnv(home string, extra map[string]string) []string {
	base := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=" + home,
		"TZ=UTC",
		"LANG=C",
		"LC_ALL=C",
		"AUTO_UPGRADE=0",
	}
	// Proxy and custom CA settings are inherited so acme.sh can reach the CA
	// in restricted networks. They never contain provider credentials.
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if v := os.Getenv(k); v != "" {
			base = append(base, k+"="+v)
		}
	}
	for k, v := range extra {
		if !validEnvKey(k) || v == "" {
			continue
		}
		base = append(base, k+"="+v)
	}
	return base
}

func validEnvKey(k string) bool {
	if k == "" {
		return false
	}
	for i, r := range k {
		if r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

func exportPaths(stageDir string) acme.ExportPaths {
	return acme.ExportPaths{
		KeyPath:       filepath.Join(stageDir, "privkey.pem"),
		CertPath:      filepath.Join(stageDir, "cert.pem"),
		ChainPath:     filepath.Join(stageDir, "chain.pem"),
		FullchainPath: filepath.Join(stageDir, "fullchain.pem"),
	}
}

func verifyExport(p acme.ExportPaths) error {
	for _, f := range []string{p.KeyPath, p.CertPath, p.ChainPath, p.FullchainPath} {
		st, err := os.Stat(f)
		if err != nil {
			return fmt.Errorf("acme.sh did not export %s: %w", filepath.Base(f), err)
		}
		if st.Size() == 0 {
			return fmt.Errorf("acme.sh exported an empty %s", filepath.Base(f))
		}
	}
	return os.Chmod(p.KeyPath, 0o600)
}

type tailBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	n, err := t.buf.Write(p)
	if t.buf.Len() > t.max {
		over := t.buf.Len() - t.max
		t.buf.Next(over)
	}
	return n, err
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.buf.String()
}

// FormatExit is a helper for doctor output.
func FormatExit(code int) string { return strconv.Itoa(code) }
