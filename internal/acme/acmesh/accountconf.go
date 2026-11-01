package acmesh

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WriteAccountConf creates a per-job 0600 account config containing only the
// account email and the auto-upgrade kill switch. DNS credentials are passed
// through the process environment and are never written here.
func WriteAccountConf(dir, email string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create accountconf dir: %w", err)
	}
	path := filepath.Join(dir, "account.conf")
	var b strings.Builder
	if email != "" {
		b.WriteString("ACCOUNT_EMAIL='" + singleQuote(email) + "'\n")
	}
	// Upgrade of acme.sh is image-managed only (D-008).
	b.WriteString("AUTO_UPGRADE='0'\n")
	b.WriteString("LOG_LEVEL='2'\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return "", fmt.Errorf("write accountconf: %w", err)
	}
	return path, nil
}

// RemoveAccountConf deletes the temporary credential file. It is safe to call
// on a missing path.
func RemoveAccountConf(path string) error {
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// InspectMainAccountConf reports SAVED_* keys that must never be persisted in
// the primary account.conf. It returns the offending key names.
func InspectMainAccountConf(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var bad []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, _, ok := strings.Cut(line, "=")
		if ok && strings.HasPrefix(strings.TrimSpace(key), "SAVED_") {
			bad = append(bad, strings.TrimSpace(key))
		}
	}
	return bad, nil
}

// ScrubSavedCredentials removes SAVED_* lines from every acme.sh .conf file
// under home. DNS plugins persist credentials there; the control plane passes
// them via environment variables instead, so they must not survive a job.
func ScrubSavedCredentials(home string) ([]string, error) {
	var touched []string
	err := filepath.WalkDir(home, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Keep the walk shallow: account.conf and <domain>[_ecc]/*.conf.
			rel, _ := filepath.Rel(home, path)
			if rel != "." && strings.Count(rel, string(filepath.Separator)) >= 2 {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".conf") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lines := strings.Split(string(data), "\n")
		out := make([]string, 0, len(lines))
		changed := false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "SAVED_") {
				changed = true
				continue
			}
			out = append(out, line)
		}
		if !changed {
			return nil
		}
		if err := os.WriteFile(path, []byte(strings.Join(out, "\n")), 0o600); err != nil {
			return err
		}
		touched = append(touched, path)
		return nil
	})
	return touched, err
}

func singleQuote(s string) string {
	return strings.ReplaceAll(s, "'", `'\''`)
}
