package acmesh

import (
	"strings"
	"testing"

	"github.com/axmishell/kpccssl/internal/acme"
)

func TestIssueArgsDNS(t *testing.T) {
	req := acme.IssueRequest{
		Domains: []string{"example.com", "*.example.com"}, KeyType: "ec-256",
		Challenge: "dns-01", DNSProvider: "dns_cf", DirectoryURL: "https://acme.example/dir",
		DNSSleep: 30, PreferredChain: "ISRG Root X1", DNSAlias: "_acme-challenge.example.com",
		AlwaysNewKey: true, MustStaple: true, Force: true, AccountConf: "/data/tmp/account.conf",
		Env: map[string]string{"CF_Token": "secret-token"},
	}
	args, err := IssueArgs(req, "/data/acme")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--issue", "--home", "/data/acme", "--server", "https://acme.example/dir", "--no-color",
		"-d", "example.com", "-d", "*.example.com", "--keylength", "ec-256",
		"--dns", "dns_cf", "--dnssleep", "30", "--challenge-alias", "_acme-challenge.example.com",
		"--preferred-chain", "ISRG Root X1", "--always-force-new-domain-key", "--ocsp-must-staple", "--force",
		"--accountconf", "/data/tmp/account.conf",
	}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args mismatch:\n got %v\nwant %v", args, want)
	}
	for _, a := range args {
		if strings.Contains(a, "secret-token") {
			t.Fatal("credential leaked into argv")
		}
	}
}

func TestIssueArgsChallenges(t *testing.T) {
	base := acme.IssueRequest{Domains: []string{"example.com"}, KeyType: "rsa-2048", DirectoryURL: "https://ca/dir"}
	cases := []struct {
		challenge string
		webroot   string
		want      string
	}{
		{"http-01-webroot", "/var/www", "--webroot"},
		{"http-01-standalone", "", "--standalone"},
		{"tls-alpn-01", "", "--alpn"},
	}
	for _, c := range cases {
		req := base
		req.Challenge, req.Webroot = c.challenge, c.webroot
		args, err := IssueArgs(req, "/home")
		if err != nil {
			t.Fatalf("%s: %v", c.challenge, err)
		}
		if !contains(args, c.want) {
			t.Fatalf("%s: missing %s in %v", c.challenge, c.want, args)
		}
	}
}

func TestIssuePorts(t *testing.T) {
	req := acme.IssueRequest{Domains: []string{"example.com"}, KeyType: "ec-256", DirectoryURL: "https://ca/dir", Challenge: "http-01-standalone", HTTPPort: 8081}
	args, err := IssueArgs(req, "/h")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(args, "--httpport") || !contains(args, "8081") {
		t.Fatalf("missing httpport: %v", args)
	}
	req.Challenge, req.TLSPort = "tls-alpn-01", 8443
	args, _ = IssueArgs(req, "/h")
	if !contains(args, "--tlsport") || !contains(args, "8443") {
		t.Fatalf("missing tlsport: %v", args)
	}
}

func TestIssueArgsValidation(t *testing.T) {
	if _, err := IssueArgs(acme.IssueRequest{}, "/h"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := IssueArgs(acme.IssueRequest{Domains: []string{"a.com"}, DirectoryURL: "https://x", Challenge: "dns-01"}, "/h"); err == nil {
		t.Fatal("dns-01 requires provider")
	}
	if _, err := IssueArgs(acme.IssueRequest{Domains: []string{"a.com"}, DirectoryURL: "https://x", Challenge: "http-01-webroot"}, "/h"); err == nil {
		t.Fatal("webroot required")
	}
}

func TestRenewArgs(t *testing.T) {
	args, err := RenewArgs(acme.RenewRequest{Domain: "example.com", KeyType: "ec-256", Force: true}, "/home")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--renew", "--home", "/home", "-d", "example.com", "--no-color", "--ecc", "--force"}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Fatalf("got %v want %v", args, want)
	}
	args, _ = RenewArgs(acme.RenewRequest{Domain: "example.com", KeyType: "rsa-2048"}, "/home")
	if contains(args, "--ecc") {
		t.Fatal("rsa must not pass --ecc")
	}
}

func TestInstallArgs(t *testing.T) {
	paths := acme.ExportPaths{KeyPath: "/s/key", CertPath: "/s/cert", ChainPath: "/s/chain", FullchainPath: "/s/full"}
	args, err := InstallArgs("example.com", "ec-384", "/home", paths)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--install-cert", "--ecc", "--key-file", "/s/key", "--cert-file", "/s/cert", "--ca-file", "/s/chain", "--fullchain-file", "/s/full"} {
		if !contains(args, want) {
			t.Fatalf("missing %q in %v", want, args)
		}
	}
}

func TestRevokeArgs(t *testing.T) {
	args, err := RevokeArgs(acme.RevokeRequest{Domain: "example.com", KeyType: "ec-256", Reason: 1}, "/home")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--revoke", "--ecc", "--revoke-reason", "1"} {
		if !contains(args, want) {
			t.Fatalf("missing %q in %v", want, args)
		}
	}
	if _, err := RevokeArgs(acme.RevokeRequest{}, "/home"); err == nil {
		t.Fatal("expected error without domain or cert")
	}
}

func TestRegisterArgs(t *testing.T) {
	args, err := RegisterArgs(acme.RegisterRequest{DirectoryURL: "https://ca/dir", Email: "a@b.c"}, "/home")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--register-account", "--server", "https://ca/dir", "-m", "a@b.c"} {
		if !contains(args, want) {
			t.Fatalf("missing %q", want)
		}
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
