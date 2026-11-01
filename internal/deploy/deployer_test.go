package deploy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/axmishell/kpccssl/internal/store"
)

func TestValidateWebhookURL(t *testing.T) {
	cases := []struct {
		url string
		ok  bool
	}{
		{"https://1.1.1.1/hook", true},
		{"http://example.com/hook", false},
		{"https://127.0.0.1/hook", false},
		{"https://[::1]/hook", false},
		{"https://10.0.0.1/hook", false},
		{"https://192.168.1.10/hook", false},
		{"ftp://example.com", false},
		{"not a url", false},
	}
	for _, c := range cases {
		err := ValidateWebhookURL(c.url)
		if (err == nil) != c.ok {
			t.Fatalf("%s: err=%v want ok=%v", c.url, err, c.ok)
		}
	}
}

func TestSafeJoin(t *testing.T) {
	root := t.TempDir()
	good, err := safeJoin(root, "cert.pem")
	if err != nil || good != filepath.Join(root, "cert.pem") {
		t.Fatalf("good=%q err=%v", good, err)
	}
	for _, bad := range []string{"../outside", "/etc/passwd", "..", "a/../../b"} {
		if _, err := safeJoin(root, bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestSafeDirAllowList(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "certs")
	if err := os.MkdirAll(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	d := &Deployer{AllowDirs: []string{root}}
	if _, err := d.safeDir(inside); err != nil {
		t.Fatal(err)
	}
	if _, err := d.safeDir(outside); err == nil {
		t.Fatal("directory outside allow-list accepted")
	}
}

func TestAtomicCopyAndMode(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := atomicCopy(src, dst, 0o640, true); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(dst)
	if string(data) != "secret" {
		t.Fatalf("content=%q", data)
	}
	st, _ := os.Stat(dst)
	if st.Mode().Perm() != 0o640 {
		t.Fatalf("mode=%o", st.Mode().Perm())
	}
	if err := atomicCopy(src, dst, 0o640, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dst + ".bak"); err != nil {
		t.Fatal("backup not created")
	}
}

func TestParseMode(t *testing.T) {
	if m, err := parseMode("0640"); err != nil || m != 0o640 {
		t.Fatalf("m=%o err=%v", m, err)
	}
	if _, err := parseMode("rw-r-----"); err == nil {
		t.Fatal("invalid mode accepted")
	}
}

func TestLocalFileForcesKeyMode(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "out")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(root, "src")
	if err := os.MkdirAll(src, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name string) string {
		p := filepath.Join(src, name)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	rev := &store.Revision{
		ID: "r1", CertPath: write("cert.pem"), KeyPath: write("key.pem"),
		ChainPath: write("chain.pem"), FullchainPath: write("full.pem"),
	}
	d := &Deployer{AllowDirs: []string{root}}
	err := d.localFile(LocalFileConfig{
		Directory: target, CertFile: "cert.pem", KeyFile: "privkey.pem",
		ChainFile: "chain.pem", FullchainFile: "fullchain.pem", Mode: "0644",
	}, rev)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(target, "privkey.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("private key mode = %o, want 0600", st.Mode().Perm())
	}
	st, _ = os.Stat(filepath.Join(target, "cert.pem"))
	if st.Mode().Perm() != 0o644 {
		t.Fatalf("cert mode = %o, want 0644", st.Mode().Perm())
	}
}
