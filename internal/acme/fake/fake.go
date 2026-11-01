// Package fake is an in-process ACME engine used for local development and
// end-to-end tests. It never talks to a CA.
package fake

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/axmishell/kpccssl/internal/acme"
)

// Engine implements acme.Engine without external processes.
type Engine struct {
	mu      sync.Mutex
	entries map[string]acme.ListEntry
}

// New returns an empty fake engine.
func New() *Engine { return &Engine{entries: map[string]acme.ListEntry{}} }

// RegisterAccount is a no-op.
func (e *Engine) RegisterAccount(ctx context.Context, req acme.RegisterRequest, log io.Writer) error {
	fmt.Fprintf(log, "[fake] register account %s for %s\n", req.Email, req.DirectoryURL)
	return nil
}

// Issue creates a self-signed certificate covering req.Domains.
func (e *Engine) Issue(ctx context.Context, req acme.IssueRequest, stageDir string, log io.Writer) (acme.ExportPaths, error) {
	if len(req.Domains) == 0 {
		return acme.ExportPaths{}, fmt.Errorf("no domains")
	}
	fmt.Fprintf(log, "[fake] issuing %s (%s)\n", strings.Join(req.Domains, ","), req.KeyType)
	paths, err := writeSelfSigned(req.Domains, req.KeyType, stageDir)
	if err != nil {
		return paths, err
	}
	e.mu.Lock()
	e.entries[req.Domains[0]] = acme.ListEntry{
		MainDomain: req.Domains[0],
		KeyLength:  req.KeyType,
		SAN:        req.Domains,
		Profile:    "",
		CA:         req.DirectoryURL,
		Created:    time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		Renew:      time.Now().UTC().Add(60 * 24 * time.Hour).Format("2006-01-02T15:04:05Z"),
	}
	e.mu.Unlock()
	return paths, nil
}

// Renew re-issues the certificate and bumps its expiry.
func (e *Engine) Renew(ctx context.Context, req acme.RenewRequest, stageDir string, log io.Writer) (acme.ExportPaths, error) {
	e.mu.Lock()
	entry, ok := e.entries[req.Domain]
	e.mu.Unlock()
	if !ok {
		entry = acme.ListEntry{MainDomain: req.Domain, KeyLength: req.KeyType, SAN: []string{req.Domain}}
	}
	fmt.Fprintf(log, "[fake] renewing %s\n", req.Domain)
	paths, err := writeSelfSigned(entry.SAN, req.KeyType, stageDir)
	if err != nil {
		return paths, err
	}
	entry.Created = time.Now().UTC().Format("2006-01-02T15:04:05Z")
	entry.Renew = time.Now().UTC().Add(60 * 24 * time.Hour).Format("2006-01-02T15:04:05Z")
	e.mu.Lock()
	e.entries[req.Domain] = entry
	e.mu.Unlock()
	return paths, nil
}

// Revoke is a no-op.
func (e *Engine) Revoke(ctx context.Context, req acme.RevokeRequest, log io.Writer) error {
	fmt.Fprintf(log, "[fake] revoke %s reason=%d\n", req.Domain, req.Reason)
	return nil
}

// List returns the in-memory certificate list.
func (e *Engine) List(ctx context.Context, log io.Writer) ([]acme.ListEntry, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]acme.ListEntry, 0, len(e.entries))
	for _, v := range e.entries {
		out = append(out, v)
	}
	return out, nil
}

// Version reports a fake pinned version.
func (e *Engine) Version(ctx context.Context) (string, error) { return "fake-acme 1.0.0", nil }

func writeSelfSigned(domains []string, keyType, dir string) (acme.ExportPaths, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return acme.ExportPaths{}, err
	}
	var priv any
	var pub any
	if strings.HasPrefix(keyType, "rsa") {
		bits := 2048
		if keyType == "rsa-4096" {
			bits = 4096
		}
		k, err := rsa.GenerateKey(rand.Reader, bits)
		if err != nil {
			return acme.ExportPaths{}, err
		}
		priv, pub = k, &k.PublicKey
	} else {
		curve := elliptic.P256()
		if keyType == "ec-384" {
			curve = elliptic.P384()
		}
		k, err := ecdsa.GenerateKey(curve, rand.Reader)
		if err != nil {
			return acme.ExportPaths{}, err
		}
		priv, pub = k, &k.PublicKey
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return acme.ExportPaths{}, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: domains[0], Organization: []string{"KPCCSSL Fake"}},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     domains,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
	if err != nil {
		return acme.ExportPaths{}, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return acme.ExportPaths{}, err
	}
	paths := acme.ExportPaths{
		KeyPath:       filepath.Join(dir, "privkey.pem"),
		CertPath:      filepath.Join(dir, "cert.pem"),
		ChainPath:     filepath.Join(dir, "chain.pem"),
		FullchainPath: filepath.Join(dir, "fullchain.pem"),
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(paths.KeyPath, keyPEM, 0o600); err != nil {
		return paths, err
	}
	if err := os.WriteFile(paths.CertPath, certPEM, 0o644); err != nil {
		return paths, err
	}
	if err := os.WriteFile(paths.ChainPath, certPEM, 0o644); err != nil {
		return paths, err
	}
	if err := os.WriteFile(paths.FullchainPath, append(certPEM, certPEM...), 0o644); err != nil {
		return paths, err
	}
	return paths, nil
}
