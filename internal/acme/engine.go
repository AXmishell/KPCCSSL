package acme

import (
	"context"
	"io"
)

// Engine is the ACME execution abstraction. The current implementation shells
// out to acme.sh; a future lego engine can implement the same contract.
type Engine interface {
	RegisterAccount(ctx context.Context, req RegisterRequest, log io.Writer) error
	Issue(ctx context.Context, req IssueRequest, stageDir string, log io.Writer) (ExportPaths, error)
	Renew(ctx context.Context, req RenewRequest, stageDir string, log io.Writer) (ExportPaths, error)
	Revoke(ctx context.Context, req RevokeRequest, log io.Writer) error
	List(ctx context.Context, log io.Writer) ([]ListEntry, error)
	Version(ctx context.Context) (string, error)
}

// Forgetter optionally removes acme.sh state for a certificate that is being
// deleted. Implementations must only touch their own home directory.
type Forgetter interface {
	Forget(ctx context.Context, domain, keyType string) error
}

// RegisterRequest registers or updates an ACME account for a directory URL.
type RegisterRequest struct {
	DirectoryURL string
	Email        string
	EABKid       string
	EABHMAC      string
	AccountConf  string // optional isolated account config path
}

// IssueRequest contains only whitelisted acme.sh switches. Credentials travel
// via Env, never argv.
type IssueRequest struct {
	Domains        []string
	KeyType        string
	Challenge      string // dns-01 | http-01-webroot | http-01-standalone | tls-alpn-01
	Webroot        string
	DNSProvider    string // e.g. dns_cf
	Env            map[string]string
	DirectoryURL   string
	DNSSleep       int
	PreferredChain string
	DNSAlias       string
	AlwaysNewKey   bool
	MustStaple     bool
	Force          bool
	AccountConf    string
	HTTPPort       int
	TLSPort        int
}

// RenewRequest renews an existing certificate inside the acme home.
type RenewRequest struct {
	Domain      string
	KeyType     string
	Env         map[string]string
	Force       bool
	Challenge   string
	Webroot     string
	DNSSleep    int
	AccountConf string
	HTTPPort    int
	TLSPort     int
}

// RevokeRequest revokes an existing certificate.
type RevokeRequest struct {
	Domain         string
	KeyType        string
	Reason         int
	Env            map[string]string
	DirectoryURL   string
	RevokeCertPath string // preferred when available; also works after acme.sh state loss
}

// ExportPaths points at the atomically exported certificate files.
type ExportPaths struct {
	KeyPath       string
	CertPath      string
	ChainPath     string
	FullchainPath string
}

// ListEntry is one parsed row of `acme.sh --list --listraw`.
type ListEntry struct {
	MainDomain string
	KeyLength  string
	SAN        []string
	Profile    string
	CA         string
	Created    string
	Renew      string
}

// KeyLength returns the acme.sh key length switch for a UI key type.
func KeyLength(keyType string) string {
	switch keyType {
	case "ec-256", "ec-384", "rsa-2048", "rsa-3072", "rsa-4096":
		return keyType
	}
	return "ec-256"
}

// IsECC reports whether --ecc is required for the key type.
func IsECC(keyType string) bool {
	return keyType == "ec-256" || keyType == "ec-384"
}
