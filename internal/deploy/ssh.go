package deploy

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// SSHConfig copies PEM files to a remote host over SFTP and optionally runs
// one of the fixed reload templates. Arbitrary remote commands are not
// supported.
type SSHConfig struct {
	Host          string `json:"host"`
	Port          int    `json:"port"`
	User          string `json:"user"`
	Password      string `json:"password"`
	PrivateKey    string `json:"private_key"`
	Passphrase    string `json:"passphrase"`
	HostKeySHA256 string `json:"host_key_sha256"`
	// InsecureIgnoreHostKey is an explicit escape hatch for hosts whose key is
	// not known yet. It should stay disabled in production.
	InsecureIgnoreHostKey bool `json:"insecure_ignore_host_key"`

	Directory     string `json:"directory"`
	CertFile      string `json:"cert_file"`
	KeyFile       string `json:"key_file"`
	ChainFile     string `json:"chain_file"`
	FullchainFile string `json:"fullchain_file"`
	Mode          string `json:"mode"`
	Reload        string `json:"reload"`
}

func (c SSHConfig) validateAuth() error {
	if strings.TrimSpace(c.Host) == "" {
		return errors.New("host is required")
	}
	if c.Port == 0 {
		c.Port = 22
	}
	if c.Port < 1 || c.Port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	if strings.TrimSpace(c.User) == "" {
		return errors.New("user is required")
	}
	if strings.TrimSpace(c.Password) == "" && strings.TrimSpace(c.PrivateKey) == "" {
		return errors.New("password or private_key is required")
	}
	if c.HostKeySHA256 == "" && !c.InsecureIgnoreHostKey {
		return errors.New("host_key_sha256 is required unless insecure_ignore_host_key is enabled")
	}
	if c.HostKeySHA256 != "" && !strings.HasPrefix(strings.TrimSpace(c.HostKeySHA256), "SHA256:") {
		return errors.New("host_key_sha256 must use the SHA256:... format")
	}
	return nil
}

func (c SSHConfig) validate() error {
	if err := c.validateAuth(); err != nil {
		return err
	}
	if !path.IsAbs(c.Directory) {
		return errors.New("directory must be an absolute path")
	}
	for _, part := range strings.Split(c.Directory, "/") {
		if part == ".." {
			return errors.New("directory must not contain ..")
		}
	}
	for _, name := range []string{c.CertFile, c.KeyFile, c.ChainFile, c.FullchainFile} {
		if name == "" {
			continue
		}
		if _, err := safeRemoteName(name); err != nil {
			return err
		}
	}
	if c.Mode != "" {
		if _, err := parseMode(c.Mode); err != nil {
			return fmt.Errorf("invalid mode: %w", err)
		}
	}
	if c.Reload != "" && c.Reload != "none" {
		if _, ok := sshReloadCommands[c.Reload]; !ok {
			return errors.New("unsupported reload target")
		}
	}
	return nil
}

var sshReloadCommands = map[string]string{
	"nginx":          "nginx -s reload",
	"systemd-nginx":  "systemctl reload nginx",
	"systemd-apache": "systemctl reload apache2",
	"apache":         "apachectl graceful",
}

func safeRemoteName(name string) (string, error) {
	if path.IsAbs(name) {
		return "", errors.New("file name must be relative")
	}
	clean := path.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("file name escapes directory")
	}
	return clean, nil
}

func buildSSHAuth(c SSHConfig) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod
	if c.PrivateKey != "" {
		var (
			signer ssh.Signer
			err    error
		)
		if c.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(c.PrivateKey), []byte(c.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(c.PrivateKey))
		}
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if c.Password != "" {
		methods = append(methods, ssh.Password(c.Password))
	}
	if len(methods) == 0 {
		return nil, errors.New("no SSH authentication method configured")
	}
	return methods, nil
}

func sshHostKeyCallback(c SSHConfig) (ssh.HostKeyCallback, error) {
	if c.InsecureIgnoreHostKey {
		return ssh.InsecureIgnoreHostKey(), nil
	}
	want := strings.TrimSpace(c.HostKeySHA256)
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		got := ssh.FingerprintSHA256(key)
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			return fmt.Errorf("host key mismatch: got %s, want %s", got, want)
		}
		return nil
	}, nil
}

func dialSSH(ctx context.Context, c SSHConfig) (*ssh.Client, error) {
	auth, err := buildSSHAuth(c)
	if err != nil {
		return nil, err
	}
	hostKey, err := sshHostKeyCallback(c)
	if err != nil {
		return nil, err
	}
	port := c.Port
	if port == 0 {
		port = 22
	}
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(c.Host, strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("dial SSH %s: %w", c.Host, err)
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, net.JoinHostPort(c.Host, strconv.Itoa(port)), &ssh.ClientConfig{
		User:            c.User,
		Auth:            auth,
		HostKeyCallback: hostKey,
		Timeout:         15 * time.Second,
	})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("SSH handshake: %w", err)
	}
	return ssh.NewClient(sshConn, chans, reqs), nil
}

// TestSSH validates credentials and the SFTP subsystem without changing files.
func TestSSH(ctx context.Context, c SSHConfig) error {
	if err := c.validate(); err != nil {
		return err
	}
	client, err := dialSSH(ctx, c)
	if err != nil {
		return err
	}
	defer client.Close()
	sc, err := sftp.NewClient(client)
	if err != nil {
		return fmt.Errorf("open SFTP subsystem: %w", err)
	}
	defer sc.Close()
	if _, err := sc.RealPath("."); err != nil {
		return fmt.Errorf("SFTP check failed: %w", err)
	}
	return nil
}

// TestSSHCredential checks only the host/auth fields, because a credential is
// stored separately from the target directory and reload settings.
func TestSSHCredential(ctx context.Context, c SSHConfig) error {
	if err := c.validateAuth(); err != nil {
		return err
	}
	client, err := dialSSH(ctx, c)
	if err != nil {
		return err
	}
	defer client.Close()
	sc, err := sftp.NewClient(client)
	if err != nil {
		return fmt.Errorf("open SFTP subsystem: %w", err)
	}
	defer sc.Close()
	if _, err := sc.RealPath("."); err != nil {
		return fmt.Errorf("SFTP check failed: %w", err)
	}
	return nil
}

// DeploySSH uploads the bundle atomically and runs the configured reload
// command. The reload command is always one of sshReloadCommands.
func DeploySSH(ctx context.Context, c SSHConfig, b *Bundle) error {
	if err := c.validate(); err != nil {
		return err
	}
	client, err := dialSSH(ctx, c)
	if err != nil {
		return err
	}
	defer client.Close()

	sc, err := sftp.NewClient(client)
	if err != nil {
		return fmt.Errorf("open SFTP subsystem: %w", err)
	}
	defer sc.Close()

	if err := sc.MkdirAll(path.Clean(c.Directory)); err != nil {
		return fmt.Errorf("create remote directory: %w", err)
	}
	resolvedDir, err := sc.RealPath(path.Clean(c.Directory))
	if err != nil {
		return fmt.Errorf("resolve remote directory: %w", err)
	}
	if !path.IsAbs(resolvedDir) {
		return fmt.Errorf("remote directory did not resolve to an absolute path")
	}

	mode := os.FileMode(0o640)
	if c.Mode != "" {
		mode, err = parseMode(c.Mode)
		if err != nil {
			return err
		}
	}

	files := []struct {
		name string
		data []byte
		key  bool
	}{
		{c.CertFile, b.Cert, false},
		{c.KeyFile, b.Key, true},
		{c.ChainFile, b.Chain, false},
		{c.FullchainFile, b.Fullchain, false},
	}
	for _, f := range files {
		if f.name == "" {
			continue
		}
		name, err := safeRemoteName(f.name)
		if err != nil {
			return err
		}
		dst := path.Join(resolvedDir, name)
		parent := path.Dir(dst)
		if err := sc.MkdirAll(parent); err != nil {
			return fmt.Errorf("create remote directory %s: %w", parent, err)
		}
		realParent, err := sc.RealPath(parent)
		if err != nil {
			return fmt.Errorf("resolve remote directory %s: %w", parent, err)
		}
		if realParent != resolvedDir && !strings.HasPrefix(realParent+"/", resolvedDir+"/") {
			return fmt.Errorf("remote file %s escapes deployment directory", name)
		}
		fileMode := mode
		if f.key {
			fileMode = 0o600
		}
		if err := sftpWriteAtomic(sc, path.Join(realParent, path.Base(name)), f.data, fileMode); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}

	if c.Reload != "" && c.Reload != "none" {
		if err := runSSHReload(ctx, client, c.Reload); err != nil {
			return err
		}
	}
	return nil
}

func sftpWriteAtomic(sc *sftp.Client, dst string, data []byte, mode os.FileMode) error {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return err
	}
	tmp := path.Join(path.Dir(dst), ".kpccssl-"+hex.EncodeToString(suffix[:]))
	f, err := sc.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = sc.Remove(tmp)
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		_ = sc.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = sc.Remove(tmp)
		return err
	}
	if err := sc.PosixRename(tmp, dst); err != nil {
		// Some servers do not implement posix-rename. Fall back to remove +
		// rename; this is not atomic but keeps deployments working.
		_ = sc.Remove(dst)
		if err := sc.Rename(tmp, dst); err != nil {
			_ = sc.Remove(tmp)
			return err
		}
	}
	return nil
}

func runSSHReload(ctx context.Context, client *ssh.Client, target string) error {
	cmd, ok := sshReloadCommands[target]
	if !ok {
		return errors.New("unsupported reload target")
	}
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("open SSH session: %w", err)
	}
	defer session.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = session.Close()
		case <-done:
		}
	}()
	out, err := session.CombinedOutput(cmd)
	if err != nil {
		return fmt.Errorf("reload failed: %s", truncate(string(out), 500))
	}
	return nil
}
