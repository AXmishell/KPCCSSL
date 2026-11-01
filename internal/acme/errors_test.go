package acme

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		output string
		code   string
	}{
		{"Error: too many certificates already issued for example.com", CodeRateLimit},
		{"Invalid Access Key ID", CodeDNSAuth},
		{"Timed out waiting for DNS TXT record _acme-challenge.example.com", CodeDNSPropagation},
		{"bind: address already in use", CodePortInUse},
		{"Connection refused while validating http-01", CodeHTTPChallenge},
		{"urn:ietf:params:acme:error:rejectedIdentifier", CodeCARejected},
		{"network is unreachable", CodeNetwork},
	}
	for _, c := range cases {
		err := Classify(c.output, 1)
		if err == nil {
			t.Fatalf("%q: expected error", c.output)
		}
		if err.Code != c.code {
			t.Fatalf("%q: got %s want %s", c.output, err.Code, c.code)
		}
	}
	if Classify("anything", 0) != nil {
		t.Fatal("exit 0 must not classify as error")
	}
	if got := Classify("something unexplained", 1); got.Code != CodeACMEInternal {
		t.Fatalf("got %s", got.Code)
	}
}

func TestLastLineStripsTimestamp(t *testing.T) {
	err := Classify("[Sun Nov  1 12:00:00 UTC 2026] Please add the TXT record", 1)
	if err.Code != CodeDNSPropagation {
		t.Fatalf("got %s: %s", err.Code, err.Message)
	}
	if err.Message == "" || err.Message[0] == '[' {
		t.Fatalf("message not cleaned: %q", err.Message)
	}
}
