package acmesh

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleScript = `#!/usr/bin/env sh
CA_LETSENCRYPT_V2="https://acme-v02.api.letsencrypt.org/directory"
CA_LETSENCRYPT_V2_TEST="https://acme-staging-v02.api.letsencrypt.org/directory"
CA_ZEROSSL="https://acme.zerossl.com/v2/DV90"
CA_SSLCOM_RSA="https://acme.ssl.com/sslcom-dv-rsa"
CA_SSLCOM_ECC="https://acme.ssl.com/sslcom-dv-ecc"
CA_GOOGLE="https://dv.acme-v02.api.pki.goog/directory"
CA_BUYPASS="https://api.buypass.com/acme/directory"

DEFAULT_CA=$CA_ZEROSSL

CA_NAMES="
ZeroSSL.com,zerossl
LetsEncrypt.org,letsencrypt
LetsEncrypt.org_test,letsencrypt_test,letsencrypttest
SSL.com,sslcom
Google.com,google
Buypass.com,buypass
"

CA_SERVERS="$CA_ZEROSSL,$CA_LETSENCRYPT_V2,$CA_LETSENCRYPT_V2_TEST,$CA_SSLCOM_RSA,$CA_GOOGLE,$CA_BUYPASS"
`

func writeScript(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "acme.sh")
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDiscoverCAPresets(t *testing.T) {
	presets, err := DiscoverCAPresets(writeScript(t, sampleScript))
	if err != nil {
		t.Fatalf("DiscoverCAPresets: %v", err)
	}

	want := []struct {
		id   string
		name string
		url  string
		eab  bool
	}{
		{"zerossl", "ZeroSSL", "https://acme.zerossl.com/v2/DV90", true},
		{"letsencrypt", "Let's Encrypt", "https://acme-v02.api.letsencrypt.org/directory", false},
		{"sslcom", "SSL.com (RSA)", "https://acme.ssl.com/sslcom-dv-rsa", true},
		{"sslcom-ecc", "SSL.com (ECC)", "https://acme.ssl.com/sslcom-dv-ecc", true},
		{"google", "Google Trust Services", "https://dv.acme-v02.api.pki.goog/directory", true},
		{"buypass", "Buypass.com", "https://api.buypass.com/acme/directory", false},
	}
	if len(presets) != len(want) {
		t.Fatalf("got %d presets, want %d: %+v", len(presets), len(want), presets)
	}
	for index, expected := range want {
		got := presets[index]
		if got.ID != expected.id || got.Name != expected.name || got.DirectoryURL != expected.url ||
			got.NeedsEAB != expected.eab {
			t.Errorf("preset %d = %+v, want %+v", index, got, expected)
		}
	}
}

func TestDiscoverCAPresetsErrors(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"missing names", "CA_SERVERS=\"$CA_LE\"\nCA_LE=\"https://ca/dir\"\n"},
		{"missing servers", "CA_NAMES=\"Example.com,example\"\n"},
		{"count mismatch", "CA_NAMES=\"Example.com,example\nOther.com,other\"\nCA_SERVERS=\"https://ca/dir\"\n"},
		{"unresolved server", "CA_NAMES=\"Example.com,example\"\nCA_SERVERS=\"$MISSING\"\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := DiscoverCAPresets(writeScript(t, c.content)); err == nil {
				t.Fatal("expected an error")
			}
		})
	}

	if _, err := DiscoverCAPresets(filepath.Join(t.TempDir(), "nope.sh")); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

// The bundled acme.sh is the production source of truth; make sure the parser
// keeps up with it when present.
func TestDiscoverCAPresetsFromBundledScript(t *testing.T) {
	const bundled = "/opt/acme.sh/acme.sh"
	if _, err := os.Stat(bundled); err != nil {
		t.Skip("bundled acme.sh not installed")
	}
	presets, err := DiscoverCAPresets(bundled)
	if err != nil {
		t.Fatalf("DiscoverCAPresets: %v", err)
	}
	ids := map[string]bool{}
	for _, preset := range presets {
		ids[preset.ID] = true
	}
	for _, required := range []string{"letsencrypt", "zerossl", "sslcom", "google", "actalis"} {
		if !ids[required] {
			t.Errorf("missing preset %q in %+v", required, presets)
		}
	}
}
