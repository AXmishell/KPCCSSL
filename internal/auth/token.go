package auth

import (
	"crypto/sha256"
)

// NewToken returns a URL-safe random bearer token. Tokens are sent to users by
// email and must only be stored as HashToken(token).
func NewToken() (string, error) { return randomToken(32) }

// HashToken returns the SHA-256 digest used for token lookups.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
