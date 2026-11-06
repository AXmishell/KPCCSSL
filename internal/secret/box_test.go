package secret

import (
	"bytes"
	"encoding/base64"
	"os"
	"testing"
)

func testBox(t *testing.T) *Box {
	t.Helper()
	b, err := New(map[int][]byte{1: bytes.Repeat([]byte{7}, 32)}, 1)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRoundTrip(t *testing.T) {
	b := testBox(t)
	ct, err := b.Encrypt([]byte("s3cret"), "dns_credentials:abc:config")
	if err != nil {
		t.Fatal(err)
	}
	pt, err := b.Decrypt(ct, "dns_credentials:abc:config")
	if err != nil {
		t.Fatal(err)
	}
	if string(pt) != "s3cret" {
		t.Fatalf("got %q", pt)
	}
}

func TestKeyringRotation(t *testing.T) {
	path := t.TempDir() + "/master.key"
	b, err := LoadOrCreate(path, "")
	if err != nil {
		t.Fatal(err)
	}
	oldCT, err := b.Encrypt([]byte("old"), "x")
	if err != nil {
		t.Fatal(err)
	}
	version, err := RotateFile(path)
	if err != nil || version != 2 {
		t.Fatalf("version=%d err=%v", version, err)
	}
	rotated, err := LoadOrCreate(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Version() != 2 {
		t.Fatalf("current version = %d", rotated.Version())
	}
	if got, err := rotated.Decrypt(oldCT, "x"); err != nil || string(got) != "old" {
		t.Fatalf("old ciphertext: %q err=%v", got, err)
	}
	newCT, err := rotated.Encrypt([]byte("new"), "x")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix([]byte(newCT), []byte("v2:")) {
		t.Fatalf("new ciphertext not v2: %s", newCT)
	}
}

func TestLegacyBase64KeyWithPadding(t *testing.T) {
	path := t.TempDir() + "/master.key"
	// 32 bytes whose base64 form ends in '=' (legacy format).
	raw := bytes.Repeat([]byte{0xfb}, 32)
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(raw)), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreate(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if b.Version() != 1 {
		t.Fatalf("version = %d", b.Version())
	}
	if _, err := b.Encrypt([]byte("x"), "a"); err != nil {
		t.Fatal(err)
	}
}

func TestWrongAAD(t *testing.T) {
	b := testBox(t)
	ct, _ := b.Encrypt([]byte("x"), "a")
	if _, err := b.Decrypt(ct, "b"); err == nil {
		t.Fatal("expected failure on wrong AAD")
	}
}

func TestTampered(t *testing.T) {
	b := testBox(t)
	ct, _ := b.Encrypt([]byte("hello world"), "a")
	raw := []byte(ct)
	raw[len(raw)-2] ^= 1
	if _, err := b.Decrypt(string(raw), "a"); err == nil {
		t.Fatal("expected failure on tampered ciphertext")
	}
}

func TestRotation(t *testing.T) {
	b := testBox(t)
	old, _ := b.Encrypt([]byte("old"), "f")
	if err := b.AddKey(2, bytes.Repeat([]byte{9}, 32)); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Decrypt(old, "f"); err != nil {
		t.Fatalf("old version must remain readable: %v", err)
	}
	fresh, _ := b.Encrypt([]byte("new"), "f")
	if fresh[:2] != "v2" {
		t.Fatalf("expected v2, got %s", fresh[:2])
	}
}
