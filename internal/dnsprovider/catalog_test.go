package dnsprovider

import "testing"

func TestValidateConfig(t *testing.T) {
	if err := ValidateConfig("dns_cf", map[string]string{"CF_Token": "abc"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfig("dns_cf", map[string]string{}); err == nil {
		t.Fatal("missing required field accepted")
	}
	if err := ValidateConfig("dns_cf", map[string]string{"CF_Token": "abc", "EVIL": "x"}); err == nil {
		t.Fatal("unknown field accepted")
	}
	if err := ValidateConfig("nope", map[string]string{}); err == nil {
		t.Fatal("unknown provider accepted")
	}
	if err := ValidateConfig("dns_cf", map[string]string{"CF_Token": "line\nbreak"}); err == nil {
		t.Fatal("control characters accepted")
	}
}

func TestCatalog(t *testing.T) {
	all := All()
	if len(all) < 5 {
		t.Fatalf("expected at least 5 providers, got %d", len(all))
	}
	for _, p := range all {
		if p.ID == "" || p.Name == "" || len(p.Fields) == 0 {
			t.Fatalf("incomplete provider %+v", p)
		}
		if _, ok := Get(p.ID); !ok {
			t.Fatalf("%s not retrievable", p.ID)
		}
	}
}
