package notify

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestHTTPErrorDoesNotLeakTelegramToken(t *testing.T) {
	err := &url.Error{
		Op:  "Post",
		URL: "https://api.telegram.org/bot123456:very-secret-token/sendMessage",
		Err: errors.New("connection refused"),
	}
	got := httpError("telegram request failed", err).Error()
	if strings.Contains(got, "very-secret-token") {
		t.Fatalf("token leaked: %s", got)
	}
	if !strings.Contains(got, "connection refused") {
		t.Fatalf("underlying error lost: %s", got)
	}
}

func TestValidateBotURL(t *testing.T) {
	cases := []struct {
		url   string
		hosts []string
		ok    bool
	}{
		{"https://open.feishu.cn/open-apis/bot/v2/hook/abc", []string{"open.feishu.cn"}, true},
		{"https://open.larksuite.com/open-apis/bot/v2/hook/abc", []string{"open.larksuite.com"}, true},
		{"http://open.feishu.cn/hook", []string{"open.feishu.cn"}, false},
		{"https://open.feishu.cn@evil.example/hook", []string{"open.feishu.cn"}, false},
		{"https://evil.example/hook", []string{"open.feishu.cn"}, false},
		{"https://127.0.0.1/hook", []string{"open.feishu.cn"}, false},
		{"https://open.feishu.cn:8443/hook", []string{"open.feishu.cn"}, false},
	}
	for _, c := range cases {
		err := validateBotURL(c.url, c.hosts...)
		if (err == nil) != c.ok {
			t.Fatalf("%s: err=%v want ok=%v", c.url, err, c.ok)
		}
	}
}

func TestBotSignatures(t *testing.T) {
	if feishuSign(1700000000, "secret") == "" {
		t.Fatal("empty Feishu signature")
	}
	if feishuSign(1700000000, "secret") == feishuSign(1700000001, "secret") {
		t.Fatal("Feishu signature does not include the timestamp")
	}
	if dingTalkSign(1700000000000, "secret") == dingTalkSign(1700000000001, "secret") {
		t.Fatal("DingTalk signature does not include the timestamp")
	}
	if dingTalkSign(1700000000000, "secret") == dingTalkSign(1700000000000, "other") {
		t.Fatal("DingTalk signature does not include the secret")
	}
}

func TestBotResponseError(t *testing.T) {
	if err := botResponseError([]byte(`{"errcode":0,"errmsg":"ok"}`)); err != nil {
		t.Fatalf("success rejected: %v", err)
	}
	if err := botResponseError([]byte(`{"code":0,"msg":"success"}`)); err != nil {
		t.Fatalf("success rejected: %v", err)
	}
	if err := botResponseError([]byte(`{"errcode":40001,"errmsg":"invalid token"}`)); err == nil {
		t.Fatal("error response accepted")
	}
	if err := botResponseError([]byte(`{"code":19001,"msg":"sign mismatch"}`)); err == nil {
		t.Fatal("error response accepted")
	}
}

func TestNormalizeConfigSMTPPersistsDefaults(t *testing.T) {
	n := &Notifier{}
	raw := []byte(`{"host":" smtp.example.com ","from":"Ops <ops@example.com>","to":[" a@example.com "],"username":"user","password":"pw"}`)
	out, err := n.NormalizeConfig("smtp", raw)
	if err != nil {
		t.Fatal(err)
	}
	var cfg SMTPConfig
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 587 {
		t.Fatalf("port = %d, want 587", cfg.Port)
	}
	if cfg.TLS != "starttls" {
		t.Fatalf("tls = %q, want starttls", cfg.TLS)
	}
	if cfg.Host != "smtp.example.com" {
		t.Fatalf("host = %q", cfg.Host)
	}
	if cfg.From != "ops@example.com" {
		t.Fatalf("from = %q, want bare address", cfg.From)
	}
	if len(cfg.To) != 1 || cfg.To[0] != "a@example.com" {
		t.Fatalf("to = %#v", cfg.To)
	}
}

func TestNormalizeConfigSMTPRejectsPlainAuth(t *testing.T) {
	n := &Notifier{}
	raw := []byte(`{"host":"smtp.example.com","from":"ops@example.com","to":["a@example.com"],"username":"user","tls":"none"}`)
	if _, err := n.NormalizeConfig("smtp", raw); err == nil {
		t.Fatal("plaintext auth accepted")
	}
	raw = []byte(`{"host":"smtp.example.com","from":"ops@example.com","to":["a@example.com"],"username":"user","tls":"none","allow_insecure_auth":true}`)
	if _, err := n.NormalizeConfig("smtp", raw); err != nil {
		t.Fatalf("explicit insecure auth rejected: %v", err)
	}
}

func TestNormalizeConfigSMTPUseSystemMail(t *testing.T) {
	n := &Notifier{}
	raw := []byte(`{"use_system_mail":true,"to":["ops@example.com"],"host":"ignored.example.com","username":"u","password":"p"}`)
	out, err := n.NormalizeConfig("smtp", raw)
	if err != nil {
		t.Fatal(err)
	}
	var cfg SMTPConfig
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.UseSystemMail {
		t.Fatal("use_system_mail lost")
	}
	if cfg.Host != "" || cfg.Username != "" || cfg.Password != "" {
		t.Fatalf("transport fields not cleared: %#v", cfg)
	}
	if len(cfg.To) != 1 || cfg.To[0] != "ops@example.com" {
		t.Fatalf("to = %#v", cfg.To)
	}
}

func TestPermanentError(t *testing.T) {
	base := errors.New("bad config")
	err := Permanent(base)
	if !IsPermanent(err) {
		t.Fatal("Permanent error not detected")
	}
	if !errors.Is(err, base) {
		t.Fatal("underlying error lost")
	}
	if err.Error() != base.Error() {
		t.Fatalf("message = %q", err.Error())
	}
	if got := Permanent(err); !IsPermanent(got) || got.Error() != base.Error() {
		t.Fatal("double wrapping changed the error")
	}
	if IsPermanent(base) {
		t.Fatal("plain error reported as permanent")
	}
}
