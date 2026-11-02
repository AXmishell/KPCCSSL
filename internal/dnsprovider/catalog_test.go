package dnsprovider

import (
	"os"
	"path/filepath"
	"testing"
)

const demoScript = `#!/usr/bin/env sh
dns_demo_info='Demo DNS
Site: demo.com
Docs: github.com/acmesh-official/acme.sh/wiki/dnsapi#dns_demo
Options:
 DEMO_KEY API key
 DEMO_EMAIL Account email address
OptionsAlt:
 DEMO_TOKEN API token
Optional:
 DEMO_TTL TTL in seconds (default: 300)
Notes:
 - Demo plugin
'
`

const demoJSON = `#!/usr/bin/env sh
dns_demo_info='[
  {"name":"DEMO_TOKEN","usage":"API token from the control panel","required":"1"},
  {"name":"DEMO_BASE","usage":"Defaults to https://api.demo.com","required":"0"}
]'
`

func TestValidateConfig(t *testing.T) {
	catalog := NewCatalog(curatedCatalog)
	if err := catalog.ValidateConfig("dns_cf", map[string]string{"CF_Token": "abc"}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.ValidateConfig("dns_cf", map[string]string{}); err == nil {
		t.Fatal("missing required field accepted")
	}
	if err := catalog.ValidateConfig("dns_cf", map[string]string{"CF_Token": "abc", "EVIL": "x"}); err == nil {
		t.Fatal("unknown field accepted")
	}
	if err := catalog.ValidateConfig("nope", map[string]string{}); err == nil {
		t.Fatal("unknown provider accepted")
	}
	if err := catalog.ValidateConfig("dns_cf", map[string]string{"CF_Token": "line\nbreak"}); err == nil {
		t.Fatal("control characters accepted")
	}
}

func TestCuratedCatalog(t *testing.T) {
	catalog := NewCatalog(curatedCatalog)
	all := catalog.All()
	if len(all) < 5 {
		t.Fatalf("expected at least 5 providers, got %d", len(all))
	}
	for _, p := range all {
		if p.ID == "" || p.Name == "" || len(p.Fields) == 0 {
			t.Fatalf("incomplete provider %+v", p)
		}
		if _, ok := catalog.Get(p.ID); !ok {
			t.Fatalf("%s not retrievable", p.ID)
		}
	}
}

func TestParseTextInfo(t *testing.T) {
	provider, ok := parseProvider("dns_demo", demoScript)
	if !ok {
		t.Fatal("provider not parsed")
	}
	if provider.Name != "Demo DNS" {
		t.Errorf("name = %q", provider.Name)
	}
	if provider.Docs != "https://github.com/acmesh-official/acme.sh/wiki/dnsapi#dns_demo" {
		t.Errorf("docs = %q", provider.Docs)
	}
	if !provider.Wildcard {
		t.Error("wildcard should default to true")
	}

	fields := map[string]Field{}
	for _, field := range provider.Fields {
		fields[field.Key] = field
	}
	// OptionsAlt is intentionally skipped.
	if len(fields) != 3 {
		t.Fatalf("fields = %+v", provider.Fields)
	}
	if f := fields["DEMO_KEY"]; !f.Required || !f.Secret || f.Label != "API key" {
		t.Errorf("DEMO_KEY = %+v", f)
	}
	if f := fields["DEMO_EMAIL"]; !f.Required || f.Secret {
		t.Errorf("DEMO_EMAIL = %+v", f)
	}
	if f := fields["DEMO_TTL"]; f.Required || f.Label != "TTL in seconds" || f.Help != "(default: 300)" {
		t.Errorf("DEMO_TTL = %+v", f)
	}
}

func TestParseJSONInfo(t *testing.T) {
	provider, ok := parseProvider("dns_demo", demoJSON)
	if !ok {
		t.Fatal("provider not parsed")
	}
	if provider.Name != "Demo" {
		t.Errorf("name = %q", provider.Name)
	}
	if len(provider.Fields) != 2 {
		t.Fatalf("fields = %+v", provider.Fields)
	}
	if f := provider.Fields[0]; f.Key != "DEMO_TOKEN" || !f.Required || !f.Secret || f.Label != "API token from the control panel" {
		t.Errorf("DEMO_TOKEN = %+v", f)
	}
	if f := provider.Fields[1]; f.Key != "DEMO_BASE" || f.Required {
		t.Errorf("DEMO_BASE = %+v", f)
	}
}

func TestDiscover(t *testing.T) {
	root := t.TempDir()
	dnsDir := filepath.Join(root, "dnsapi")
	if err := os.MkdirAll(dnsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "acme.sh")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dnsDir, "dns_demo.sh"), []byte(demoScript), 0o600); err != nil {
		t.Fatal(err)
	}
	// Curated metadata wins over the upstream info block.
	cloudflare := `dns_cf_info='CloudFlare
Options:
 CF_Email Email
'`
	if err := os.WriteFile(filepath.Join(dnsDir, "dns_cf.sh"), []byte(cloudflare), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dnsDir, "not-a-plugin.sh"), []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	catalog := Load(bin)
	demo, ok := catalog.Get("dns_demo")
	if !ok || demo.Name != "Demo DNS" {
		t.Fatalf("demo preset missing: %+v", demo)
	}
	cf, ok := catalog.Get("dns_cf")
	if !ok || cf.Name != "Cloudflare" || cf.Fields[0].Key != "CF_Token" {
		t.Fatalf("curated Cloudflare not used: %+v", cf)
	}
	if _, ok := catalog.Get("not-a-plugin"); ok {
		t.Fatal("non-plugin file was loaded")
	}
	// Curated providers missing from the directory are still available.
	if _, ok := catalog.Get("dns_ali"); !ok {
		t.Fatal("curated fallback provider missing")
	}
}

func TestLoadFallback(t *testing.T) {
	catalog := Load(filepath.Join(t.TempDir(), "missing", "acme.sh"))
	if len(catalog.All()) < 5 {
		t.Fatalf("expected curated fallback, got %d", len(catalog.All()))
	}
}

// Keep the parser honest against the real bundled acme.sh.
func TestDiscoverBundled(t *testing.T) {
	const bin = "/opt/acme.sh/acme.sh"
	if _, err := os.Stat(bin); err != nil {
		t.Skip("bundled acme.sh not installed")
	}
	catalog := Load(bin)
	all := catalog.All()
	if len(all) < 100 {
		t.Fatalf("expected the upstream provider list, got %d", len(all))
	}
	for _, id := range []string{"dns_cf", "dns_ali", "dns_dp", "dns_aws", "dns_gd", "dns_azure", "dns_namecheap"} {
		provider, ok := catalog.Get(id)
		if !ok {
			t.Errorf("missing provider %s", id)
			continue
		}
		if len(provider.Fields) == 0 {
			t.Errorf("%s has no fields", id)
		}
	}
}
