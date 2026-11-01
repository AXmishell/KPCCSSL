package acmesh

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseListRaw(t *testing.T) {
	out := "Main_Domain|KeyLength|SAN_Domains|Profile|CA|Created|Renew\n" +
		"example.com|\"ec-256\"|example.com,www.example.com||https://acme.example/dir|2026-01-01T00:00:00Z|2026-03-01T00:00:00Z\n" +
		"nosan.example.com|\"ec-256\"|no||https://acme.example/dir|2026-01-01T00:00:00Z|2026-03-01T00:00:00Z\n" +
		"broken|line\n"
	entries := ParseListRaw(out)
	if len(entries) != 2 {
		t.Fatalf("got %d entries", len(entries))
	}
	e := entries[0]
	if e.MainDomain != "example.com" || e.KeyLength != "ec-256" || len(e.SAN) != 2 || e.SAN[1] != "www.example.com" {
		t.Fatalf("unexpected entry %+v", e)
	}
	if len(entries[1].SAN) != 0 {
		t.Fatalf("'no' SAN should be empty: %+v", entries[1])
	}
}

func TestAccountConfNoSecrets(t *testing.T) {
	dir := t.TempDir()
	path, err := WriteAccountConf(dir, "it's@example.com")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "AUTO_UPGRADE='0'") {
		t.Fatalf("missing auto upgrade kill switch: %s", s)
	}
	if strings.Contains(s, "SAVED_") {
		t.Fatal("accountconf must not contain SAVED_* keys")
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", st.Mode().Perm())
	}
}

func TestScrubSavedCredentials(t *testing.T) {
	home := t.TempDir()
	domainDir := filepath.Join(home, "example.com_ecc")
	if err := os.MkdirAll(domainDir, 0o700); err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(domainDir, "example.com.conf")
	if err := os.WriteFile(conf, []byte("Le_Domain='example.com'\nSAVED_CF_Token='secret'\nSAVED_CF_Account_ID='abc'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	touched, err := ScrubSavedCredentials(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(touched) != 1 {
		t.Fatalf("touched = %v", touched)
	}
	data, _ := os.ReadFile(conf)
	if strings.Contains(string(data), "SAVED_") {
		t.Fatalf("SAVED_* survived: %s", data)
	}
	if !strings.Contains(string(data), "Le_Domain='example.com'") {
		t.Fatal("non-secret setting was removed")
	}
}

func TestScrubEAB(t *testing.T) {
	home := t.TempDir()
	ca := filepath.Join(home, "ca", "acme.example")
	if err := os.MkdirAll(ca, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(ca, "ca.conf")
	_ = os.WriteFile(path, []byte("CA_KEY_HASH='x'\nCA_EAB_KEY_ID='kid'\nCA_EAB_HMAC_KEY='hmac'\n"), 0o600)
	touched, err := ScrubEAB(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(touched) != 1 {
		t.Fatalf("touched = %v", touched)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "CA_EAB") {
		t.Fatalf("EAB material survived: %s", data)
	}
}

func TestValidateEnvKeys(t *testing.T) {
	for _, k := range []string{"CF_Token", "AWS_ACCESS_KEY_ID", "Ali_Key", "foo"} {
		if !validEnvKey(k) {
			t.Fatalf("valid key rejected: %q", k)
		}
	}
	for _, k := range []string{"", "FOO-BAR", "1ABC", "A B", "A=B"} {
		if validEnvKey(k) {
			t.Fatalf("invalid key accepted: %q", k)
		}
	}
}
