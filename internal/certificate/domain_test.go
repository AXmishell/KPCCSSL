package certificate

import "testing"

func TestNormalizeDomain(t *testing.T) {
	valid := map[string]string{
		"Example.COM":           "example.com",
		"example.com.":          "example.com",
		"*.Example.com":         "*.example.com",
		"xn--bcher-kva.example": "xn--bcher-kva.example",
		"bücher.example":        "xn--bcher-kva.example",
		"a-b.example.com":       "a-b.example.com",
	}
	for in, want := range valid {
		got, err := NormalizeDomain(in)
		if err != nil {
			t.Fatalf("%q: unexpected error %v", in, err)
		}
		if got != want {
			t.Fatalf("%q: got %q want %q", in, got, want)
		}
	}

	invalid := []string{"", "-example.com", "example..com", "*.com", "exa mple.com", "http://example.com", "1.2.3.4", "*.*.example.com", "exa_mple.com"}
	for _, in := range invalid {
		if _, err := NormalizeDomain(in); err == nil {
			t.Fatalf("%q: expected error", in)
		}
	}
}

func TestNormalizeNames(t *testing.T) {
	primary, sans, err := NormalizeNames("Example.com", []string{"www.example.com", "WWW.example.com", "", "*.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if primary != "example.com" {
		t.Fatalf("primary = %q", primary)
	}
	want := []string{"example.com", "www.example.com", "*.example.com"}
	if len(sans) != len(want) {
		t.Fatalf("sans = %v", sans)
	}
	for i := range want {
		if sans[i] != want[i] {
			t.Fatalf("sans[%d] = %q want %q", i, sans[i], want[i])
		}
	}
}

func TestValidateChallenge(t *testing.T) {
	if err := ValidateChallenge("dns-01", []string{"*.example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateChallenge("http-01-webroot", []string{"*.example.com"}); err == nil {
		t.Fatal("wildcard with HTTP-01 must fail")
	}
	if err := ValidateChallenge("bogus", []string{"example.com"}); err == nil {
		t.Fatal("unknown challenge must fail")
	}
}

func TestValidateKeyType(t *testing.T) {
	for _, k := range []string{"ec-256", "ec-384", "rsa-2048", "rsa-3072", "rsa-4096"} {
		if err := ValidateKeyType(k); err != nil {
			t.Fatalf("%s: %v", k, err)
		}
	}
	if err := ValidateKeyType("ed25519"); err == nil {
		t.Fatal("expected error")
	}
}

func TestRateLimitKey(t *testing.T) {
	cases := map[string]string{
		"www.example.com":   "example.com",
		"*.example.com":     "example.com",
		"a.b.example.co.uk": "example.co.uk",
	}
	for in, want := range cases {
		if got := RateLimitKey(in); got != want {
			t.Fatalf("%q: got %q want %q", in, got, want)
		}
	}
}
