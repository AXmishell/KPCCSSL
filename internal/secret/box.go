// Package secret implements envelope-free AES-256-GCM encryption for
// credentials at rest. Ciphertext format: "v1:<base64(nonce||ct||tag)>".
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Box encrypts and decrypts values with a versioned master key.
type Box struct {
	keys    map[int][]byte
	current int
}

// LoadOrCreate reads a 32-byte key from path, generating it with mode 0600
// when missing. An explicit key (base64 or 64 hex chars) takes precedence only
// when the file does not exist yet.
func LoadOrCreate(path, provided string) (*Box, error) {
	var key []byte
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		key, err = decodeKey(strings.TrimSpace(string(raw)))
		if err != nil {
			return nil, fmt.Errorf("master key %s: %w", path, err)
		}
		_ = os.Chmod(path, 0o600)
	case errors.Is(err, os.ErrNotExist):
		if provided != "" {
			key, err = decodeKey(provided)
			if err != nil {
				return nil, fmt.Errorf("KPCC_MASTER_KEY: %w", err)
			}
		} else {
			key = make([]byte, 32)
			if _, err := rand.Read(key); err != nil {
				return nil, fmt.Errorf("generate master key: %w", err)
			}
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(key)), 0o600); err != nil {
			return nil, fmt.Errorf("write master key: %w", err)
		}
	default:
		return nil, fmt.Errorf("read master key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("master key must be 32 bytes, got %d", len(key))
	}
	return &Box{keys: map[int][]byte{1: key}, current: 1}, nil
}

// New builds a Box from raw keys, keyed by version.
func New(keys map[int][]byte, current int) (*Box, error) {
	if len(keys[current]) != 32 {
		return nil, fmt.Errorf("current key version %d missing or wrong length", current)
	}
	return &Box{keys: keys, current: current}, nil
}

func decodeKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if len(s) == 64 {
		if b, err := hexDecode(s); err == nil {
			return b, nil
		}
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		b, err = base64.RawStdEncoding.DecodeString(s)
	}
	if err != nil {
		return nil, errors.New("must be base64 or 64 hex characters")
	}
	return b, nil
}

func hexDecode(s string) ([]byte, error) {
	out := make([]byte, len(s)/2)
	for i := 0; i < len(out); i++ {
		v, err := strconv.ParseUint(s[i*2:i*2+2], 16, 8)
		if err != nil {
			return nil, err
		}
		out[i] = byte(v)
	}
	return out, nil
}

// Version returns the version used for new ciphertext.
func (b *Box) Version() int { return b.current }

// Encrypt seals plaintext with AAD bound to table:record:field.
func (b *Box) Encrypt(plaintext []byte, aad string) (string, error) {
	gcm, err := b.aead(b.current)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nil, nonce, plaintext, []byte(aad))
	raw := append(nonce, sealed...)
	return "v" + strconv.Itoa(b.current) + ":" + base64.StdEncoding.EncodeToString(raw), nil
}

// Decrypt opens a value produced by Encrypt. The version prefix selects the
// key, enabling rotation.
func (b *Box) Decrypt(value, aad string) ([]byte, error) {
	version, raw, err := split(value)
	if err != nil {
		return nil, err
	}
	gcm, err := b.aead(version)
	if err != nil {
		return nil, err
	}
	if len(raw) < gcm.NonceSize() {
		return nil, errors.New("ciphertext too short")
	}
	nonce, ct := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	pt, err := gcm.Open(nil, nonce, ct, []byte(aad))
	if err != nil {
		return nil, errors.New("decrypt failed: wrong key, AAD or tampered ciphertext")
	}
	return pt, nil
}

// AddKey registers a new key version and makes it current.
func (b *Box) AddKey(version int, key []byte) error {
	if len(key) != 32 {
		return errors.New("key must be 32 bytes")
	}
	b.keys[version] = key
	b.current = version
	return nil
}

func (b *Box) aead(version int) (cipher.AEAD, error) {
	key, ok := b.keys[version]
	if !ok {
		return nil, fmt.Errorf("unknown key version %d", version)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func split(value string) (int, []byte, error) {
	prefix, rest, ok := strings.Cut(value, ":")
	if !ok || !strings.HasPrefix(prefix, "v") {
		return 0, nil, errors.New("invalid ciphertext envelope")
	}
	v, err := strconv.Atoi(prefix[1:])
	if err != nil {
		return 0, nil, errors.New("invalid key version")
	}
	raw, err := base64.StdEncoding.DecodeString(rest)
	if err != nil {
		return 0, nil, errors.New("invalid ciphertext base64")
	}
	return v, raw, nil
}
