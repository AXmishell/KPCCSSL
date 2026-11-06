package deploy

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTencentDeploymentConfig(t *testing.T) {
	base := TencentConfig{
		SecretID: "id", SecretKey: "key", Region: "ap-beijing", Domain: "a.example.com",
	}
	if err := base.validate("tencent_cdn"); err != nil {
		t.Fatalf("CDN config rejected: %v", err)
	}
	if err := base.validate("tencent_cos"); err == nil {
		t.Fatal("COS config without bucket accepted")
	}
	base.Bucket = "assets"
	if err := base.validate("tencent_cos"); err != nil {
		t.Fatalf("COS config rejected: %v", err)
	}

	for depType, want := range map[string]string{
		"tencent_cdn": "cdn",
		"tencent_waf": "waf",
		"tencent_cos": "cos",
		"tencent_teo": "teo",
	} {
		got, ok := tencentResourceType(depType)
		if !ok || got != want {
			t.Fatalf("%s resource type = %q, %v", depType, got, ok)
		}
	}
	if _, ok := tencentResourceType("tencent_bogus"); ok {
		t.Fatal("unknown resource type accepted")
	}

	ids, err := tencentInstanceIDs("tencent_cos", base)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "ap-beijing|assets|a.example.com" {
		t.Fatalf("COS ids = %#v", ids)
	}
	base.Domain = "a.example.com,b.example.com"
	ids, err = tencentInstanceIDs("tencent_cdn", base)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[1] != "b.example.com" {
		t.Fatalf("CDN ids = %#v", ids)
	}
}

func TestAliyunDeploymentConfig(t *testing.T) {
	base := AliyunConfig{
		AccessKeyID: "id", AccessKeySecret: "secret", Region: "cn-hangzhou", Domain: "a.example.com",
	}
	if err := base.validate("aliyun_cdn"); err != nil {
		t.Fatalf("CDN config rejected: %v", err)
	}
	if err := base.validate("aliyun_oss"); err == nil {
		t.Fatal("OSS config without bucket accepted")
	}
	base.Bucket = "assets"
	if err := base.validate("aliyun_oss"); err != nil {
		t.Fatalf("OSS config rejected: %v", err)
	}
	if got := ossEndpoint(""); got != "oss.aliyuncs.com" {
		t.Fatalf("global endpoint = %q", got)
	}
	if got := ossEndpoint("cn-beijing"); got != "oss-cn-beijing.aliyuncs.com" {
		t.Fatalf("regional endpoint = %q", got)
	}
	if got := ossBucketURL("assets", "cn-beijing"); got != "https://assets.oss-cn-beijing.aliyuncs.com" {
		t.Fatalf("bucket URL = %q", got)
	}
}

func TestOSSSignature(t *testing.T) {
	a := ossSign("secret", http.MethodPost, "text/xml; charset=utf-8", "Wed, 21 Oct 2015 07:28:00 GMT", "/assets/?cname&comp=add")
	b := ossSign("secret", http.MethodPost, "text/xml; charset=utf-8", "Wed, 21 Oct 2015 07:28:00 GMT", "/assets/?cname&comp=add")
	if a == "" || a != b {
		t.Fatalf("signature is not deterministic: %q %q", a, b)
	}
	if a == ossSign("other", http.MethodPost, "text/xml; charset=utf-8", "Wed, 21 Oct 2015 07:28:00 GMT", "/assets/?cname&comp=add") {
		t.Fatal("signature does not include the secret")
	}
}

func TestOSSRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "cname&comp=add" {
			t.Errorf("query = %q", r.URL.RawQuery)
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "OSS ak:") {
			t.Errorf("authorization = %q", auth)
		}
		if r.Header.Get("Date") == "" || r.Header.Get("Content-Type") == "" {
			t.Error("missing signed headers")
		}
		body, _ := io.ReadAll(r.Body)
		var cfg ossCnameConfiguration
		if err := xml.Unmarshal(body, &cfg); err != nil {
			t.Errorf("invalid XML: %v", err)
		}
		if cfg.Cname.Domain != "a.example.com" || cfg.Cname.CertificateConfiguration.Certificate != "CERT" {
			t.Errorf("body = %s", body)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	var body ossCnameConfiguration
	body.Cname.Domain = "a.example.com"
	body.Cname.CertificateConfiguration.Certificate = "CERT"
	body.Cname.CertificateConfiguration.PrivateKey = "KEY"
	body.Cname.CertificateConfiguration.Force = true
	data, err := xml.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := ossRequest(context.Background(), http.MethodPost, server.URL, "assets", "cname&comp=add", "text/xml; charset=utf-8", data, "ak", "sk"); err != nil {
		t.Fatal(err)
	}
}

func TestQiniuToken(t *testing.T) {
	token := qiniuToken("ak", "secret", "/sslcert", "")
	if !strings.HasPrefix(token, "QBox ak:") {
		t.Fatalf("token = %q", token)
	}
	if token != qiniuToken("ak", "secret", "/sslcert", "") {
		t.Fatal("token is not deterministic")
	}
	if token == qiniuToken("ak", "secret", "/sslcert", "a=1") {
		t.Fatal("token does not include the query")
	}
	if token == qiniuToken("ak", "other", "/sslcert", "") {
		t.Fatal("token does not include the secret")
	}
	// This is the value produced by the Qiniu Go SDK for the same request.
	if token != "QBox ak:l1jq7wcW4OXgp6TvRwFyHfl0WEY=" {
		t.Fatalf("token = %q", token)
	}
}

func TestDeployQiniu(t *testing.T) {
	old := qiniuAPIBase
	defer func() { qiniuAPIBase = old }()

	var binds []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "QBox ak:") {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sslcert":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["pri"] != "KEY" || body["ca"] != "CERT" {
				t.Errorf("body = %#v", body)
			}
			_, _ = w.Write([]byte(`{"certID":"cert-1"}`))
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/domain/") && strings.HasSuffix(r.URL.Path, "/sslize"):
			binds = append(binds, strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/domain/"), "/sslize"))
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["certid"] != "cert-1" {
				t.Errorf("body = %#v", body)
			}
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	qiniuAPIBase = server.URL

	cfg := QiniuConfig{AccessKey: "ak", AccessSecret: "secret", Domain: "a.example.com,b.example.com"}
	if err := DeployQiniu(context.Background(), cfg, &Bundle{Cert: []byte("CERT"), Key: []byte("KEY")}); err != nil {
		t.Fatal(err)
	}
	if len(binds) != 2 || binds[0] != "a.example.com" || binds[1] != "b.example.com" {
		t.Fatalf("binds = %#v", binds)
	}
}
