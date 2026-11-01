package certificate

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"strings"
	"time"
)

// X509Info is the subset of certificate metadata persisted in revisions.
type X509Info struct {
	Serial            string
	FingerprintSHA256 string
	NotBefore         int64
	NotAfter          int64
	DNSNames          []string
	Issuer            string
	Subject           string
}

// ParsePEMCertificate reads the first CERTIFICATE block from path.
func ParsePEMCertificate(path string) (*X509Info, *x509.Certificate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, nil, fmt.Errorf("%s: no PEM certificate block", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	sum := sha256.Sum256(cert.Raw)
	return &X509Info{
		Serial:            strings.ToLower(cert.SerialNumber.Text(16)),
		FingerprintSHA256: strings.ToUpper(hex.EncodeToString(sum[:])),
		NotBefore:         cert.NotBefore.UTC().Unix(),
		NotAfter:          cert.NotAfter.UTC().Unix(),
		DNSNames:          cert.DNSNames,
		Issuer:            cert.Issuer.String(),
		Subject:           cert.Subject.String(),
	}, cert, nil
}

// VerifyKeyMatches checks that the private key at keyPath belongs to cert.
func VerifyKeyMatches(cert *x509.Certificate, keyPath string) error {
	data, err := os.ReadFile(keyPath)
	if err != nil {
		return err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return fmt.Errorf("%s: no PEM block", keyPath)
	}
	var pub any
	switch block.Type {
	case "PRIVATE KEY":
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return err
		}
		switch key := k.(type) {
		case *ecdsa.PrivateKey:
			pub = &key.PublicKey
		case *rsa.PrivateKey:
			pub = &key.PublicKey
		case ed25519.PrivateKey:
			pub = key.Public()
		default:
			return fmt.Errorf("%s: unsupported key type", keyPath)
		}
	case "EC PRIVATE KEY":
		k, err := x509.ParseECPrivateKey(block.Bytes)
		if err != nil {
			return err
		}
		pub = &k.PublicKey
	case "RSA PRIVATE KEY":
		k, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return err
		}
		pub = &k.PublicKey
	default:
		return fmt.Errorf("%s: unsupported PEM type %s", keyPath, block.Type)
	}
	if !publicKeysEqual(cert.PublicKey, pub) {
		return fmt.Errorf("private key does not match certificate")
	}
	return nil
}

func publicKeysEqual(a, b any) bool {
	ab, err := x509.MarshalPKIXPublicKey(a)
	if err != nil {
		return false
	}
	bb, err := x509.MarshalPKIXPublicKey(b)
	if err != nil {
		return false
	}
	return bytes.Equal(ab, bb)
}

// RenewalTime returns a renewal timestamp 30 days before NotAfter, clamped to
// at least one day in the future for short-lived certificates.
func RenewalTime(notAfter int64, now time.Time) int64 {
	candidate := notAfter - 30*86400
	if candidate <= now.Unix() {
		candidate = now.Unix() + 86400
	}
	if candidate >= notAfter {
		candidate = notAfter - 3600
	}
	return candidate
}

// NamesMatch reports whether the issued SAN set covers the desired names.
func NamesMatch(desired, got []string) bool {
	set := map[string]bool{}
	for _, n := range got {
		set[strings.ToLower(n)] = true
	}
	for _, n := range desired {
		if !set[strings.ToLower(n)] {
			return false
		}
	}
	return true
}
