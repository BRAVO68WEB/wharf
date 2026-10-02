package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// EnsureRSAPrivateKey provisions the RS256 signing key at path when no key
// file exists yet, so a fresh deployment boots without an operator-supplied
// PEM. An existing file is never touched: operator-provisioned key material
// keeps its meaning across restarts (all issued tokens stay valid). The key
// is written PKCS#8 with mode 0600 and created exclusively, so a concurrent
// starter cannot half-overwrite it. It reports whether it created the file.
func EnsureRSAPrivateKey(path string) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("stat jwt private key: %w", err)
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return false, fmt.Errorf("create jwt key directory: %w", err)
		}
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return false, fmt.Errorf("generate jwt private key: %w", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return false, fmt.Errorf("marshal jwt private key: %w", err)
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("create jwt private key: %w", err)
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(path)
		if writeErr != nil {
			return false, fmt.Errorf("write jwt private key: %w", writeErr)
		}
		return false, fmt.Errorf("write jwt private key: %w", closeErr)
	}
	return true, nil
}
