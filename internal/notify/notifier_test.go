package notify

import (
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
