package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// HashAccessToken is the hex SHA-256 of a docker-login token. The plaintext
// is shown once and is not stored.
func HashAccessToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NewAccessToken returns a new token and the hash to store.
func NewAccessToken() (plain, hash string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("access token: %w", err)
	}
	// New tokens start with wharf_. A token issued earlier starts with bdr_
	// and still verifies: the stored value is a hash of the full string.
	plain = "wharf_" + hex.EncodeToString(buf)
	return plain, HashAccessToken(plain), nil
}
