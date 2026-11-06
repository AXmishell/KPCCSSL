package deploy

import (
	"fmt"
	"os"
	"strings"

	"github.com/axmishell/kpccssl/internal/store"
)

// Bundle is the certificate material handed to a deployment target. Remote
// targets only need the PEM payloads; metadata is included for verification
// and logging.
type Bundle struct {
	Cert      []byte
	Key       []byte
	Chain     []byte
	Fullchain []byte

	Fingerprint string
	NotAfter    int64
}

// bundleFromRevision loads the PEM files of a certificate revision.
func bundleFromRevision(rev *store.Revision) (*Bundle, error) {
	read := func(name, path string) ([]byte, error) {
		if path == "" {
			return nil, fmt.Errorf("revision has no %s path", name)
		}
		data, err := os.ReadFile(path) // #nosec G304 -- path comes from the revision row written by the issuer
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		return data, nil
	}

	cert, err := read("certificate", rev.CertPath)
	if err != nil {
		return nil, err
	}
	key, err := read("private key", rev.KeyPath)
	if err != nil {
		return nil, err
	}
	chain, err := read("chain", rev.ChainPath)
	if err != nil {
		return nil, err
	}
	fullchain, err := read("fullchain", rev.FullchainPath)
	if err != nil {
		return nil, err
	}

	return &Bundle{
		Cert:        []byte(strings.TrimSpace(string(cert))),
		Key:         []byte(strings.TrimSpace(string(key))),
		Chain:       []byte(strings.TrimSpace(string(chain))),
		Fullchain:   []byte(strings.TrimSpace(string(fullchain))),
		Fingerprint: rev.FingerprintSHA256,
		NotAfter:    rev.NotAfter,
	}, nil
}

// CertChain returns the fullchain when available. Panels and cloud CDN/WAF
// APIs expect the leaf certificate together with its intermediates.
func (b *Bundle) CertChain() []byte {
	if len(b.Fullchain) > 0 {
		return b.Fullchain
	}
	return b.Cert
}
