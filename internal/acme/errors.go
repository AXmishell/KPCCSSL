package acme

import (
	"fmt"
	"strings"
)

// Error codes shared by the API and the UI.
const (
	CodeDomainInvalid   = "E_DOMAIN_INVALID"
	CodeDNSAuth         = "E_DNS_AUTH"
	CodeDNSPropagation  = "E_DNS_PROPAGATION"
	CodeHTTPChallenge   = "E_HTTP_CHALLENGE"
	CodePortInUse       = "E_PORT_IN_USE"
	CodeRateLimit       = "E_RATE_LIMIT"
	CodeCARejected      = "E_CA_REJECTED"
	CodeNetwork         = "E_NETWORK"
	CodeTimeout         = "E_TIMEOUT"
	CodeACMEInternal    = "E_ACME_INTERNAL"
	CodeUnknown         = "E_UNKNOWN"
	CodeCertMissing     = "E_CERT_MISSING"
	CodeInterrupted     = "E_INTERRUPTED"
	CodeCanceled        = "E_CANCELED"
	CodeDeployFailed    = "E_DEPLOY_FAILED"
	CodeNotifyFailed    = "E_NOTIFY_FAILED"
	CodeInvalidArgument = "E_VALIDATION"
)

// Error is a classified acme.sh failure.
type Error struct {
	Code     string
	Message  string
	ExitCode int
	Output   string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Classify maps raw acme.sh output to a stable error code. Rules are ordered
// from most specific to least specific.
func Classify(output string, exitCode int) *Error {
	low := strings.ToLower(output)
	type rule struct {
		code     string
		patterns []string
	}
	rules := []rule{
		{CodeRateLimit, []string{"too many certificates", "rate limit", "rate limited", "urn:ietf:params:acme:error:ratelimited", "ratelimited"}},
		{CodeDNSAuth, []string{
			"invalid access key", "authentication failed", "unauthorized", "invalid token",
			"api token", "invalid credentials", "forbidden", "insufficient permission",
			"authentication error", "invalid domain name", "no such host", "invalid secret",
			"error getting validation data: unauthorized",
		}},
		{CodeDNSPropagation, []string{
			"timed out waiting for", "dns propagation", "_acme-challenge", "txt record",
			"not found: _acme", "no txt record", "could not find the dns record",
			"verify error", "dns not propagated",
		}},
		{CodePortInUse, []string{"address already in use", "bind: address already in use", "port 80 is in use", "cannot listen"}},
		{CodeHTTPChallenge, []string{
			"connection refused", "could not connect to", "invalid response from",
			"http-01", "404", "403", "webroot", "could not resolve host", "wrong http",
		}},
		{CodeCARejected, []string{
			"urn:ietf:params:acme:error:", "rejected", "order not ready", "csr",
			"account creation failed", "badnonce", "malformed",
		}},
		{CodeNetwork, []string{"network is unreachable", "connection timed out", "tls handshake", "certificate verify", "proxy", "dns error", "temporary failure in name resolution"}},
		{CodeTimeout, []string{"timeout", "timed out", "deadline exceeded"}},
		{CodeDomainInvalid, []string{"invalid domain", "invalid character", "domain name is invalid", "wildcard"}},
	}
	if exitCode == 0 {
		return nil
	}
	for _, r := range rules {
		for _, p := range r.patterns {
			if strings.Contains(low, p) {
				return &Error{Code: r.code, Message: lastLine(output), ExitCode: exitCode, Output: output}
			}
		}
	}
	return &Error{Code: CodeACMEInternal, Message: lastLine(output), ExitCode: exitCode, Output: output}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l != "" {
			// Strip acme.sh's timestamp prefix like "[Sun Nov  1 ...]".
			if strings.HasPrefix(l, "[") {
				if idx := strings.Index(l, "]"); idx > 0 {
					l = strings.TrimSpace(l[idx+1:])
				}
			}
			if len(l) > 500 {
				l = l[:500]
			}
			return l
		}
	}
	return "acme.sh failed"
}
