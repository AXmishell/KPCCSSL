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
