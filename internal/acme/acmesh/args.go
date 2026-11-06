package acmesh

import (
	"fmt"
	"strconv"

	"github.com/axmishell/kpccssl/internal/acme"
)

// Args builders are pure functions so they can be golden-tested. Every switch
// is whitelisted here; handlers never construct argv themselves.

// RegisterArgs builds `--register-account`.
func RegisterArgs(req acme.RegisterRequest, home string) ([]string, error) {
	if req.DirectoryURL == "" {
		return nil, fmt.Errorf("directory URL is required")
	}
	args := []string{"--register-account", "--home", home, "--server", req.DirectoryURL, "--no-color"}
	if req.Email != "" {
		args = append(args, "-m", req.Email)
	}
	if req.EABKid != "" {
		args = append(args, "--eab-kid", req.EABKid, "--eab-hmac-key", req.EABHMAC)
	}
	if req.AccountConf != "" {
		args = append(args, "--accountconf", req.AccountConf)
	}
	return args, nil
}

// IssueArgs builds `--issue`.
func IssueArgs(req acme.IssueRequest, home string) ([]string, error) {
	if len(req.Domains) == 0 {
		return nil, fmt.Errorf("at least one domain is required")
	}
	if req.DirectoryURL == "" {
		return nil, fmt.Errorf("directory URL is required")
	}
	args := []string{"--issue", "--home", home, "--server", req.DirectoryURL, "--no-color"}
	for _, d := range req.Domains {
		args = append(args, "-d", d)
	}
	args = append(args, "--keylength", acme.KeyLength(req.KeyType))

	switch req.Challenge {
	case "dns-01":
		if req.DNSProvider == "" {
			return nil, fmt.Errorf("DNS provider is required for dns-01")
		}
		args = append(args, "--dns", req.DNSProvider)
		if req.DNSSleep > 0 {
			args = append(args, "--dnssleep", strconv.Itoa(req.DNSSleep))
		}
		if req.DNSAlias != "" {
			args = append(args, "--challenge-alias", req.DNSAlias)
		}
	case "http-01-webroot":
		if req.Webroot == "" {
			return nil, fmt.Errorf("webroot is required for http-01-webroot")
		}
		args = append(args, "--webroot", req.Webroot)
	case "http-01-standalone":
		args = append(args, "--standalone")
		if req.HTTPPort > 0 {
			args = append(args, "--httpport", strconv.Itoa(req.HTTPPort))
		}
	case "tls-alpn-01":
		args = append(args, "--alpn")
		if req.TLSPort > 0 {
			args = append(args, "--tlsport", strconv.Itoa(req.TLSPort))
		}
	default:
		return nil, fmt.Errorf("unsupported challenge %q", req.Challenge)
	}

	if req.PreferredChain != "" {
		args = append(args, "--preferred-chain", req.PreferredChain)
	}
	if req.AlwaysNewKey {
		args = append(args, "--always-force-new-domain-key")
	}
	if req.MustStaple {
		args = append(args, "--ocsp-must-staple")
	}
	if req.Force {
		args = append(args, "--force")
	}
	if req.AccountConf != "" {
		args = append(args, "--accountconf", req.AccountConf)
	}
	return args, nil
}

// RenewArgs builds `--renew` for an existing domain in the acme home.
func RenewArgs(req acme.RenewRequest, home string) ([]string, error) {
	if req.Domain == "" {
		return nil, fmt.Errorf("domain is required")
	}
	args := []string{"--renew", "--home", home, "-d", req.Domain, "--no-color"}
	if acme.IsECC(req.KeyType) {
		args = append(args, "--ecc")
	}
	if req.Force {
		args = append(args, "--force")
	}
	if req.DNSSleep > 0 {
		args = append(args, "--dnssleep", strconv.Itoa(req.DNSSleep))
	}
	if req.AccountConf != "" {
		args = append(args, "--accountconf", req.AccountConf)
	}
	if req.Challenge == "http-01-standalone" && req.HTTPPort > 0 {
		args = append(args, "--httpport", strconv.Itoa(req.HTTPPort))
	}
	if req.Challenge == "tls-alpn-01" && req.TLSPort > 0 {
		args = append(args, "--tlsport", strconv.Itoa(req.TLSPort))
	}
	return args, nil
}

// InstallArgs builds `--install-cert` into a staging directory.
func InstallArgs(domain, keyType, home string, out acme.ExportPaths) ([]string, error) {
	if domain == "" {
		return nil, fmt.Errorf("domain is required")
	}
	args := []string{"--install-cert", "--home", home, "-d", domain, "--no-color"}
	if acme.IsECC(keyType) {
		args = append(args, "--ecc")
	}
	args = append(args,
		"--key-file", out.KeyPath,
		"--cert-file", out.CertPath,
		"--ca-file", out.ChainPath,
		"--fullchain-file", out.FullchainPath,
	)
	return args, nil
}

// RevokeArgs builds `--revoke`.
func RevokeArgs(req acme.RevokeRequest, home string) ([]string, error) {
	args := []string{"--revoke", "--home", home, "--no-color"}
	if req.DirectoryURL != "" {
		args = append(args, "--server", req.DirectoryURL)
	}
	switch {
	case req.RevokeCertPath != "":
		// The exported revision is authoritative and still works when the
		// acme.sh domain directory was lost or deleted.
		args = append(args, "--cert-file", req.RevokeCertPath)
	case req.Domain != "":
		args = append(args, "-d", req.Domain)
		if acme.IsECC(req.KeyType) {
			args = append(args, "--ecc")
		}
	default:
		return nil, fmt.Errorf("domain or certificate path is required")
	}
	if req.Reason >= 0 && req.Reason <= 10 {
		args = append(args, "--revoke-reason", strconv.Itoa(req.Reason))
	}
	return args, nil
}

// ListArgs builds `--list --listraw`.
func ListArgs(home string) []string {
	return []string{"--list", "--listraw", "--home", home, "--no-color"}
}

// VersionArgs asks acme.sh for its version.
func VersionArgs() []string {
	return []string{"--version", "--no-color"}
}
