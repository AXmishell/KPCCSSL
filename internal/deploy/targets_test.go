package deploy

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestSSHConfigValidate(t *testing.T) {
	base := SSHConfig{
		Host: "example.com", User: "deploy", Password: "x",
		HostKeySHA256: "SHA256:abc", Directory: "/etc/certs",
		CertFile: "cert.pem", KeyFile: "key.pem",
	}
	if err := base.validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	missingHostKey := base
	missingHostKey.HostKeySHA256 = ""
	if err := missingHostKey.validate(); err == nil {
		t.Fatal("config without host key was accepted")
	}
	missingHostKey.InsecureIgnoreHostKey = true
	if err := missingHostKey.validate(); err != nil {
		t.Fatalf("explicit insecure host key rejected: %v", err)
	}
	badDir := base
	badDir.Directory = "relative/certs"
	if err := badDir.validate(); err == nil {
		t.Fatal("relative directory accepted")
	}
	badName := base
	badName.CertFile = "../cert.pem"
	if err := badName.validate(); err == nil {
		t.Fatal("path traversal accepted")
	}
	badReload := base
	badReload.Reload = "rm -rf /"
	if err := badReload.validate(); err == nil {
		t.Fatal("arbitrary reload command accepted")
	}
	noAuth := base
	noAuth.Password = ""
	if err := noAuth.validate(); err == nil {
		t.Fatal("config without auth accepted")
	}
}

func TestSSHHostKeyCallback(t *testing.T) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := ssh.NewPublicKey(private.Public())
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := ssh.FingerprintSHA256(pub)
	cb, err := sshHostKeyCallback(SSHConfig{HostKeySHA256: fingerprint})
	if err != nil {
		t.Fatal(err)
	}
	if err := cb("host", nil, pub); err != nil {
		t.Fatalf("pinned key rejected: %v", err)
	}
	otherPubKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherPub, err := ssh.NewPublicKey(otherPubKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := cb("host", nil, otherPub); err == nil {
		t.Fatal("unpinned key accepted")
	}
}

func TestBaotaToken(t *testing.T) {
	got := btToken("1700000000", "secret")
	if len(got) != 32 {
		t.Fatalf("token length = %d", len(got))
	}
	if got != btToken("1700000000", "secret") {
		t.Fatal("token is not deterministic")
	}
	if got == btToken("1700000001", "secret") {
		t.Fatal("token did not include the timestamp")
	}
}

func TestOnePanelToken(t *testing.T) {
	got := onePanelToken("1700000000", "secret")
	if len(got) != 32 {
		t.Fatalf("token length = %d", len(got))
	}
	if got == onePanelToken("1700000000", "other") {
		t.Fatal("token did not include the API key")
	}
}

func TestJoinPanelURLPreservesQuery(t *testing.T) {
	got, err := joinPanelURL("https://panel.example.com:8888/", "system?action=GetNetWork")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://panel.example.com:8888/system?action=GetNetWork" {
		t.Fatalf("url = %q", got)
	}
}

func TestDeployBaotaPanelAndSite(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		if r.Form.Get("request_token") == "" {
			t.Error("missing request_token")
		}
		paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
		switch r.URL.Path {
		case "/config":
			if r.URL.Query().Get("action") != "SetPanelSSL" {
				t.Errorf("unexpected action: %s", r.URL.RawQuery)
			}
			if r.Form.Get("privateKey") == "" || r.Form.Get("certPem") == "" {
				t.Error("certificate material missing")
			}
			_, _ = w.Write([]byte(`{"status":true}`))
		case "/ssl/cert/save_cert":
			_, _ = w.Write([]byte(`{"status":true,"ssl_hash":"hash-1"}`))
		case "/ssl":
			if r.URL.Query().Get("action") != "SetBatchCertToSite" {
				t.Errorf("unexpected action: %s", r.URL.RawQuery)
			}
			var batch []map[string]string
			if err := json.Unmarshal([]byte(r.Form.Get("BatchInfo")), &batch); err != nil {
				t.Errorf("bad BatchInfo: %v", err)
			}
			if len(batch) != 2 || batch[0]["siteName"] != "a.example.com" || batch[0]["ssl_hash"] != "hash-1" {
				t.Errorf("batch = %#v", batch)
			}
			_, _ = w.Write([]byte(`{"status":true}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	b := &Bundle{Cert: []byte("CERT"), Key: []byte("KEY")}
	cfg := BaotaConfig{URL: server.URL, APIKey: "key"}
	if err := DeployBaota(context.Background(), "baota_panel", cfg, b); err != nil {
		t.Fatal(err)
	}
	siteCfg := BaotaConfig{URL: server.URL, APIKey: "key", SiteNames: []string{"a.example.com", "b.example.com"}}
	if err := DeployBaota(context.Background(), "baota_site", siteCfg, b); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 3 {
		t.Fatalf("paths = %#v", paths)
	}
}

func TestDeployOnePanelPanelAndSite(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("1Panel-Token") == "" || r.Header.Get("1Panel-Timestamp") == "" {
			t.Error("missing 1Panel auth headers")
		}
		switch {
		case r.URL.Path == "/api/v1/settings/ssl/update":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["cert"] != "CERT" || body["key"] != "KEY" {
				t.Errorf("body = %#v", body)
			}
			_, _ = w.Write([]byte(`{"code":200}`))
		case r.URL.Path == "/api/v1/websites/7/https" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"code":200,"data":{"SSLProtocol":["TLSv1.3"],"algorithm":"RSA","hsts":true,"httpConfig":"HTTPToHTTPS"}}`))
		case r.URL.Path == "/api/v1/websites/7/https" && r.Method == http.MethodPost:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["certificate"] != "CERT" || body["websiteId"] != float64(7) {
				t.Errorf("body = %#v", body)
			}
			_, _ = w.Write([]byte(`{"code":200}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	b := &Bundle{Cert: []byte("CERT"), Key: []byte("KEY")}
	cfg := OnePanelConfig{URL: server.URL, APIKey: "key"}
	if err := DeployOnePanel(context.Background(), "1panel_panel", cfg, b); err != nil {
		t.Fatal(err)
	}
	siteCfg := OnePanelConfig{URL: server.URL, APIKey: "key", SiteID: "7"}
	if err := DeployOnePanel(context.Background(), "1panel_site", siteCfg, b); err != nil {
		t.Fatal(err)
	}
}

func TestDeployOnePanelV2Path(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/core/settings/ssl/update" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["ssl"] != "Enable" {
			t.Errorf("ssl = %v", body["ssl"])
		}
		_, _ = w.Write([]byte(`{"code":200}`))
	}))
	defer server.Close()

	cfg := OnePanelConfig{URL: server.URL, APIKey: "key", Version: "v2"}
	if err := DeployOnePanel(context.Background(), "1panel_panel", cfg, &Bundle{Cert: []byte("CERT"), Key: []byte("KEY")}); err != nil {
		t.Fatal(err)
	}
}

func TestAliyunPercentEncode(t *testing.T) {
	if got := aliyunPercentEncode("a b*c~d"); got != "a%20b%2Ac~d" {
		t.Fatalf("encoded = %q", got)
	}
}

func TestFlattenAliyunParams(t *testing.T) {
	out := url.Values{}
	flattenAliyunParams("Listen", map[string]any{
		"CertId": "abc",
		"Ports":  []any{float64(80), float64(443)},
		"TLS":    true,
	}, out)
	if out.Get("Listen.CertId") != "abc" || out.Get("Listen.Ports.1") != "80" || out.Get("Listen.TLS") != "true" {
		t.Fatalf("got = %#v", out)
	}
}

func TestAliyunRPCRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		for _, key := range []string{"AccessKeyId", "Action", "Signature", "SignatureNonce", "Timestamp", "Version"} {
			if r.Form.Get(key) == "" {
				t.Errorf("missing %s", key)
			}
		}
		_, _ = w.Write([]byte(`{"RequestId":"1"}`))
	}))
	defer server.Close()

	if _, err := aliyunRPC(context.Background(), server.URL, "2018-05-10", "DescribeCdnService", "id", "secret", nil); err != nil {
		t.Fatal(err)
	}
}

func TestHMACSHA256(t *testing.T) {
	got := hmacSHA256([]byte("key"), "The quick brown fox jumps over the lazy dog")
	if len(got) != 32 {
		t.Fatalf("len = %d", len(got))
	}
}

func TestDeployerTestRejectsUnknownType(t *testing.T) {
	d := &Deployer{}
	if err := d.Test(context.Background(), "bogus", `{}`); err == nil {
		t.Fatal("unknown type accepted")
	}
}
