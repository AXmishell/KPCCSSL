package acmesh

import (
	"os"
	"path/filepath"
	"strings"
)

// ScrubEAB removes CA_EAB_* lines from acme.sh CA config files after a
// successful registration. The HMAC is kept only in the encrypted database and
// is supplied again if the account is re-registered.
func ScrubEAB(home string) ([]string, error) {
	caDir := filepath.Join(home, "ca")
	entries, err := os.ReadDir(caDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var touched []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(caDir, e.Name(), "ca.conf")
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var kept []string
		changed := false
		for _, line := range strings.Split(string(data), "\n") {
			t := strings.TrimSpace(line)
			if strings.HasPrefix(t, "CA_EAB_KEY_ID") || strings.HasPrefix(t, "CA_EAB_HMAC_KEY") {
				changed = true
				continue
			}
			kept = append(kept, line)
		}
		if changed {
			if err := os.WriteFile(path, []byte(strings.Join(kept, "\n")), 0o600); err != nil { // #nosec G703 -- path comes from WalkDir under the acme home
				return touched, err
			}
			touched = append(touched, path)
		}
	}
	return touched, nil
}
