// Package dnsprovider builds the DNS credential catalog exposed by the UI and
// validates credential field names before they are used as environment
// variables for acme.sh.
//
// Providers and their credential fields are discovered from the dnsapi
// directory of the bundled acme.sh (the dns_<id>_info blocks), so new upstream
// plugins appear without code changes. A curated catalog is used as a fallback
// and to keep precise labels/permissions for the most common providers.
package dnsprovider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Field describes one credential input.
type Field struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Secret   bool   `json:"secret"`
	Required bool   `json:"required"`
	Help     string `json:"help,omitempty"`
}

// Provider describes an acme.sh DNS plugin.
type Provider struct {
	ID       string  `json:"id"`       // acme.sh plugin name, e.g. dns_cf
	Name     string  `json:"name"`     // human name
	Docs     string  `json:"docs"`     // upstream docs URL
	MinRole  string  `json:"min_role"` // permission guidance
	Fields   []Field `json:"fields"`
	Wildcard bool    `json:"wildcard"`
}

// Catalog is an immutable set of providers.
type Catalog struct {
	providers []Provider
	byID      map[string]Provider
}

// Load builds a catalog from the acme.sh installation next to acmeBin. When
// the dnsapi directory is unavailable it falls back to the curated catalog.
func Load(acmeBin string) *Catalog {
	discovered, ok := discover(filepath.Join(filepath.Dir(acmeBin), "dnsapi"))
	if !ok {
		return NewCatalog(curatedCatalog)
	}
	seen := make(map[string]bool, len(discovered))
	for _, p := range discovered {
		seen[p.ID] = true
	}
	for _, p := range curatedCatalog {
		if !seen[p.ID] {
			discovered = append(discovered, p)
		}
	}
	return NewCatalog(discovered)
}

// NewCatalog indexes and sorts a provider list.
func NewCatalog(providers []Provider) *Catalog {
	out := append([]Provider(nil), providers...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].ID < out[j].ID
		}
		return out[i].Name < out[j].Name
	})
	byID := make(map[string]Provider, len(out))
	for _, p := range out {
		byID[p.ID] = p
	}
	return &Catalog{providers: out, byID: byID}
}

// All returns every provider sorted by display name.
func (c *Catalog) All() []Provider {
	return append([]Provider(nil), c.providers...)
}

// Get returns a provider by acme.sh plugin id.
func (c *Catalog) Get(id string) (Provider, bool) {
	p, ok := c.byID[id]
	return p, ok
}

// ValidateConfig checks that every supplied key belongs to the provider and
// that required keys are present. Unknown keys are rejected so users cannot
// inject arbitrary environment variables into acme.sh.
func (c *Catalog) ValidateConfig(providerID string, cfg map[string]string) error {
	p, ok := c.byID[providerID]
	if !ok {
		return &ValidationError{Msg: "unknown provider " + providerID}
	}
	allowed := make(map[string]Field, len(p.Fields))
	for _, f := range p.Fields {
		allowed[f.Key] = f
	}
	for k := range cfg {
		if _, ok := allowed[k]; !ok {
			return &ValidationError{Msg: "unknown field " + k + " for provider " + providerID}
		}
	}
	for _, f := range p.Fields {
		if f.Required && cfg[f.Key] == "" {
			return &ValidationError{Msg: "missing required field " + f.Key}
		}
	}
	for k, v := range cfg {
		if len(v) > 8192 {
			return &ValidationError{Msg: "field " + k + " is too long"}
		}
		if strings.ContainsAny(v, "\x00\r\n") {
			return &ValidationError{Msg: "field " + k + " contains control characters"}
		}
	}
	return nil
}

// ValidationError is returned for invalid provider configuration.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

// --- curated fallback -----------------------------------------------------

var curatedCatalog = []Provider{
	{
		ID: "dns_cf", Name: "Cloudflare", Docs: "https://github.com/acmesh-official/acme.sh/wiki/dnsapi#dns_cf",
		MinRole:  "API Token with Zone:DNS:Edit for the target zones",
		Wildcard: true,
		Fields: []Field{
			{Key: "CF_Token", Label: "API Token", Secret: true, Required: true, Help: "Zone → DNS → Edit"},
			{Key: "CF_Account_ID", Label: "Account ID", Secret: false, Help: "Optional; recommended with scoped tokens"},
			{Key: "CF_Zone_ID", Label: "Zone ID", Secret: false, Help: "Optional; pins the token to one zone"},
		},
	},
	{
		ID: "dns_ali", Name: "Aliyun DNS", Docs: "https://github.com/acmesh-official/acme.sh/wiki/dnsapi#dns_ali",
		MinRole:  "RAM user with AliyunDNSFullAccess (or scoped AliyunDNSRead/Write)",
		Wildcard: true,
		Fields: []Field{
			{Key: "Ali_Key", Label: "AccessKey ID", Secret: false, Required: true},
			{Key: "Ali_Secret", Label: "AccessKey Secret", Secret: true, Required: true},
		},
	},
	{
		ID: "dns_dp", Name: "DNSPod (Tencent Cloud)", Docs: "https://github.com/acmesh-official/acme.sh/wiki/dnsapi#dns_dp",
		MinRole:  "DNSPod API token with domain record permissions",
		Wildcard: true,
		Fields: []Field{
			{Key: "DP_Id", Label: "Token ID", Secret: false, Required: true},
			{Key: "DP_Key", Label: "Token", Secret: true, Required: true},
		},
	},
	{
		ID: "dns_aws", Name: "AWS Route 53", Docs: "https://github.com/acmesh-official/acme.sh/wiki/dnsapi#dns_aws",
		MinRole:  "IAM user with route53:ChangeResourceRecordSets on the hosted zone",
		Wildcard: true,
		Fields: []Field{
			{Key: "AWS_ACCESS_KEY_ID", Label: "Access Key ID", Secret: false, Required: true},
			{Key: "AWS_SECRET_ACCESS_KEY", Label: "Secret Access Key", Secret: true, Required: true},
			{Key: "AWS_REGION", Label: "Region", Secret: false, Help: "Default: us-east-1"},
		},
	},
	{
		ID: "dns_gd", Name: "GoDaddy", Docs: "https://github.com/acmesh-official/acme.sh/wiki/dnsapi#dns_gd",
		MinRole:  "Production API key + secret with DNS edit access",
		Wildcard: true,
		Fields: []Field{
			{Key: "GD_Key", Label: "API Key", Secret: false, Required: true},
			{Key: "GD_Secret", Label: "API Secret", Secret: true, Required: true},
		},
	},
}

// --- acme.sh discovery ------------------------------------------------------

var infoBlockRE = regexp.MustCompile(`(?ms)dns_[A-Za-z0-9_]+_info='(.*?)'`)

var fieldKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func discover(dir string) ([]Provider, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, false
	}
	curated := make(map[string]Provider, len(curatedCatalog))
	for _, p := range curatedCatalog {
		curated[p.ID] = p
	}

	providers := make([]Provider, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "dns_") || !strings.HasSuffix(name, ".sh") {
			continue
		}
		id := strings.TrimSuffix(name, ".sh")
		if p, ok := curated[id]; ok {
			providers = append(providers, p)
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		if p, ok := parseProvider(id, string(data)); ok {
			providers = append(providers, p)
		}
	}
	if len(providers) == 0 {
		return nil, false
	}
	return providers, true
}

func parseProvider(id, script string) (Provider, bool) {
	match := infoBlockRE.FindStringSubmatch(script)
	if match == nil {
		return Provider{}, false
	}
	info := strings.TrimSpace(match[1])
	provider := Provider{ID: id, Name: prettyName(id), Wildcard: true}

	if strings.HasPrefix(info, "[") || strings.HasPrefix(info, "{") {
		parseJSONInfo(&provider, info)
	} else {
		parseTextInfo(&provider, info)
	}
	if len(provider.Fields) == 0 {
		return Provider{}, false
	}
	if provider.Name == "" {
		provider.Name = prettyName(id)
	}
	return provider, true
}

func parseTextInfo(provider *Provider, info string) {
	lines := strings.Split(info, "\n")
	if len(lines) > 0 {
		if first := strings.TrimSpace(lines[0]); first != "" {
			provider.Name = first
		}
	}

	section := ""
	for _, line := range lines {
		text := strings.TrimSpace(line)
		if text == "" {
			continue
		}
		switch {
		case text == "Options:":
			section = "required"
			continue
		case text == "OptionsAlt:":
			// Alternative configuration sets are skipped: they would show up
			// as a second, conflicting set of inputs.
			section = ""
			continue
		case text == "Optional:":
			section = "optional"
			continue
		case isInfoSection(text):
			section = ""
		}

		if strings.HasPrefix(text, "Docs:") {
			provider.Docs = normalizeDocs(strings.TrimSpace(strings.TrimPrefix(text, "Docs:")))
			continue
		}
		if section == "" {
			continue
		}

		key, description := splitFieldLine(text)
		if key == "" {
			continue
		}
		required := section == "required" && !optionalDescription(description)
		label, help := splitLabel(description)
		provider.Fields = append(provider.Fields, Field{
			Key: key, Label: label, Secret: isSecretKey(key), Required: required, Help: help,
		})
	}
}

func parseJSONInfo(provider *Provider, info string) {
	var fields []struct {
		Name     string `json:"name"`
		Usage    string `json:"usage"`
		Required string `json:"required"`
	}
	if err := json.Unmarshal([]byte(info), &fields); err == nil {
		for _, f := range fields {
			if !fieldKeyRE.MatchString(f.Name) {
				continue
			}
			label, help := splitLabel(f.Usage)
			provider.Fields = append(provider.Fields, Field{
				Key: f.Name, Label: label, Secret: isSecretKey(f.Name),
				Required: f.Required == "1" || strings.EqualFold(f.Required, "true"), Help: help,
			})
		}
		return
	}

	var object struct {
		Name    string `json:"name"`
		Docs    string `json:"docs"`
		Options []struct {
			Name     string `json:"name"`
			Usage    string `json:"usage"`
			Required string `json:"required"`
		} `json:"options"`
	}
	if err := json.Unmarshal([]byte(info), &object); err != nil {
		return
	}
	if object.Name != "" {
		provider.Name = object.Name
	}
	provider.Docs = normalizeDocs(object.Docs)
	for _, f := range object.Options {
		if !fieldKeyRE.MatchString(f.Name) {
			continue
		}
		label, help := splitLabel(f.Usage)
		provider.Fields = append(provider.Fields, Field{
			Key: f.Name, Label: label, Secret: isSecretKey(f.Name),
			Required: f.Required == "1" || strings.EqualFold(f.Required, "true"), Help: help,
		})
	}
}

// isInfoSection reports metadata headers that terminate an Options block.
func isInfoSection(line string) bool {
	for _, header := range []string{
		"Site:", "Domains:", "Docs:", "Signature:", "Issues:", "Author:",
		"Notes:", "Note:", "Requires:", "Description:",
	} {
		if strings.HasPrefix(line, header) {
			return true
		}
	}
	return false
}

func splitFieldLine(line string) (key, description string) {
	fields := strings.Fields(line)
	if len(fields) == 0 || !fieldKeyRE.MatchString(fields[0]) {
		return "", ""
	}
	key = fields[0]
	description = strings.TrimSpace(strings.TrimPrefix(line, key))
	return key, description
}

func splitLabel(description string) (label, help string) {
	description = strings.TrimSpace(description)
	if description == "" {
		return "", ""
	}
	if index := strings.Index(description, ". "); index > 0 && index <= 48 {
		return description[:index], strings.TrimSpace(description[index+2:])
	}
	if index := strings.Index(description, " ("); index > 0 && index <= 48 {
		return description[:index], strings.TrimSpace(description[index+1:])
	}
	if len(description) > 48 {
		if index := strings.Index(description, "."); index > 0 {
			return description[:index], strings.TrimSpace(description[index+1:])
		}
		return description[:47] + "…", description
	}
	return strings.TrimSuffix(description, "."), ""
}

func optionalDescription(description string) bool {
	lower := strings.ToLower(description)
	for _, marker := range []string{
		"optional", "default:", "default ", "required only if", "only if",
		"instead of", "not required", "not saved", "\"true\"/\"false\"",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func isSecretKey(key string) bool {
	upper := strings.ToUpper(key)
	for _, marker := range []string{"SECRET", "TOKEN", "PASSWORD", "PASSWD", "PRIVATE", "CREDENTIAL", "HMAC"} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	if strings.HasSuffix(upper, "_KEY") && !strings.HasSuffix(upper, "_KEY_ID") && !strings.HasSuffix(upper, "_KID") {
		return true
	}
	return strings.HasSuffix(upper, "_SK")
}

func normalizeDocs(docs string) string {
	docs = strings.TrimSpace(docs)
	if docs == "" {
		return ""
	}
	if strings.HasPrefix(docs, "http://") || strings.HasPrefix(docs, "https://") {
		return docs
	}
	return "https://" + docs
}

func prettyName(id string) string {
	name := strings.TrimPrefix(id, "dns_")
	name = strings.ReplaceAll(name, "_", " ")
	if name == "" {
		return id
	}
	return strings.ToUpper(name[:1]) + name[1:]
}
