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
	plain = "bdr_" + hex.EncodeToString(buf)
	return plain, HashAccessToken(plain), nil
}
