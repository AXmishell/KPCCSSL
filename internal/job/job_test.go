package job

import (
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	cases := []string{
		"Authorization: Bearer abcdef",
		"CF_Token=supersecret",
		"password: hunter2",
		"-----BEGIN PRIVATE KEY-----\nMIIE\n-----END PRIVATE KEY-----",
		"Cookie: session=abc",
	}
	for _, in := range cases {
		out := Redact(in)
		if out == in {
			t.Fatalf("not redacted: %q", in)
		}
		for _, secret := range []string{"abcdef", "supersecret", "hunter2", "MIIE", "session=abc"} {
			if strings.Contains(out, secret) {
				t.Fatalf("%q leaked in %q", secret, out)
			}
		}
	}
	if got := Redact("normal log line"); got != "normal log line" {
		t.Fatalf("normal line changed: %q", got)
	}
}

func TestRetryDelay(t *testing.T) {
	if d := RetryDelay("issue", 1); d.Minutes() != 1 {
		t.Fatalf("issue attempt 1 = %s", d)
	}
	if d := RetryDelay("renew", 4); d.Hours() != 24 {
		t.Fatalf("renew attempt 4 = %s", d)
	}
	if d := RetryDelay("revoke", 1); d != 0 {
		t.Fatalf("revoke delay = %s", d)
	}
}
