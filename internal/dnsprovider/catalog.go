// Package dnsprovider holds the static DNS provider catalog exposed by the UI
// and validates credential field names before they are used as environment
// variables for acme.sh.
package dnsprovider

import (
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

var catalog = []Provider{
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

var byID = func() map[string]Provider {
	m := make(map[string]Provider, len(catalog))
	for _, p := range catalog {
		m[p.ID] = p
	}
	return m
}()

// All returns the catalog sorted by display name.
func All() []Provider {
	out := append([]Provider(nil), catalog...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns a provider by acme.sh plugin id.
func Get(id string) (Provider, bool) {
	p, ok := byID[id]
	return p, ok
}

// ValidateConfig checks that every supplied key belongs to the provider and
// that required keys are present. Unknown keys are rejected so users cannot
// inject arbitrary environment variables into acme.sh.
func ValidateConfig(providerID string, cfg map[string]string) error {
	p, ok := byID[providerID]
	if !ok {
		return &ValidationError{Msg: "unknown provider " + providerID}
	}
	allowed := map[string]Field{}
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
