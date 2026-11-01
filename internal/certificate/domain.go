// Package certificate contains domain validation, x509 inspection and the
// certificate state helpers.
package certificate

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

// ValidationError describes a rejected input field; API maps it to 422.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Message }

// NormalizeDomain lowercases, IDNA-encodes and validates a DNS name. A leading
// "*." wildcard is allowed only for names with at least three labels.
func NormalizeDomain(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", &ValidationError{"domain", "must not be empty"}
	}
	if strings.HasPrefix(s, "-") || strings.HasPrefix(s, ".") {
		return "", &ValidationError{"domain", "must not start with '-' or '.'"}
	}
	if strings.ContainsAny(s, " \t\r\n/\\@:,#$%&'\"()[]{}|<>?*") && !strings.HasPrefix(s, "*.") {
		return "", &ValidationError{"domain", "contains invalid characters"}
	}
	wildcard := strings.HasPrefix(s, "*.")
	base := strings.TrimPrefix(s, "*.")
	base = strings.ToLower(strings.TrimSuffix(base, "."))
	if net.ParseIP(base) != nil {
		return "", &ValidationError{"domain", "IP addresses are not supported"}
	}
	ascii, err := idna.Lookup.ToASCII(base)
	if err != nil {
		return "", &ValidationError{"domain", "invalid internationalized name"}
	}
	if len(ascii) > 253 {
		return "", &ValidationError{"domain", "name is longer than 253 characters"}
	}
	labels := strings.Split(ascii, ".")
	if wildcard && len(labels) < 2 {
		return "", &ValidationError{"domain", "wildcard requires a registrable domain (e.g. *.example.com)"}
	}
	for _, l := range labels {
		if len(l) == 0 || len(l) > 63 {
			return "", &ValidationError{"domain", "label length must be 1-63"}
		}
		if strings.HasPrefix(l, "-") || strings.HasSuffix(l, "-") {
			return "", &ValidationError{"domain", "labels must not start or end with '-'"}
		}
		for _, r := range l {
			if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' {
				return "", &ValidationError{"domain", "invalid character in label"}
			}
		}
	}
	if wildcard {
		return "*." + ascii, nil
	}
	return ascii, nil
}

// NormalizeNames validates the primary domain and SANs, removes duplicates and
// guarantees the primary domain is present and first.
func NormalizeNames(primary string, sans []string) (string, []string, error) {
	p, err := NormalizeDomain(primary)
	if err != nil {
		return "", nil, err
	}
	seen := map[string]bool{p: true}
	out := []string{p}
	for i, raw := range sans {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		n, err := NormalizeDomain(raw)
		if err != nil {
			if ve, ok := err.(*ValidationError); ok {
				ve.Field = fmt.Sprintf("san[%d]", i)
				return "", nil, ve
			}
			return "", nil, err
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return p, out, nil
}

// HasWildcard reports whether any name is a wildcard.
func HasWildcard(names []string) bool {
	for _, n := range names {
		if strings.HasPrefix(n, "*.") {
			return true
		}
	}
	return false
}

// ValidateChallenge enforces the compatibility matrix between challenge type
// and certificate names.
func ValidateChallenge(challenge string, names []string) error {
	switch challenge {
	case "dns-01", "http-01-webroot", "http-01-standalone", "tls-alpn-01":
	default:
		return &ValidationError{"challenge_type", "unsupported challenge type"}
	}
	if HasWildcard(names) && challenge != "dns-01" {
		return &ValidationError{"challenge_type", "wildcard certificates require DNS-01"}
	}
	return nil
}

// ValidateKeyType checks the key type whitelist.
func ValidateKeyType(k string) error {
	switch k {
	case "ec-256", "ec-384", "rsa-2048", "rsa-3072", "rsa-4096":
		return nil
	}
	return &ValidationError{"key_type", "must be one of ec-256, ec-384, rsa-2048, rsa-3072, rsa-4096"}
}

// RateLimitKey returns the registrable domain (eTLD+1) used for issuing rate
// limits. IPs and single-label names fall back to the normalized name.
func RateLimitKey(domain string) string {
	d := strings.TrimPrefix(domain, "*.")
	if etld, err := publicsuffix.EffectiveTLDPlusOne(d); err == nil {
		return etld
	}
	return d
}

// ErrNoCommonName is returned when a certificate has no SAN.
var ErrNoCommonName = errors.New("certificate has no DNS names")
