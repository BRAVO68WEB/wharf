package auth_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BRAVO68WEB/wharf/internal/auth"
	"github.com/BRAVO68WEB/wharf/internal/config"
)

// Test_EnsureRSAPrivateKey_when_Missing_then_GeneratesLoadableKey pins the
// fresh-deploy path: no operator-provisioned key file, so one is generated
// and the signer loads it without any manual step.
func Test_EnsureRSAPrivateKey_when_Missing_then_GeneratesLoadableKey(t *testing.T) {
	// Given: a key path whose parent directory exists but has no key
	dir := t.TempDir()
	path := filepath.Join(dir, "jwt_private.pem")

	// When: the key is provisioned and the signer loaded
	created, err := auth.EnsureRSAPrivateKey(path)
	if err != nil {
		t.Fatalf("EnsureRSAPrivateKey: %v", err)
	}
	signer, err := auth.LoadSigner(config.JWTConfig{
		PrivateKeyPath: path,
		Issuer:         jwtTestIssuer,
		Service:        jwtTestService,
		TokenTTL:       config.Duration(jwtTestTTL),
	})

	// Then: the key was created and both signing and validation work
	if err != nil {
		t.Fatalf("LoadSigner over generated key: %v", err)
	}
	if !created {
		t.Error("created = false, want true when no key file exists")
	}
	tok, err := signer.Sign(signer.NewClaims("alice", nil))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := signer.Validate(tok); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

// Test_EnsureRSAPrivateKey_when_FileMissing_then_CreatesParentDirectory
// pins that provisioning works for a fresh data volume whose key directory
// does not exist yet.
func Test_EnsureRSAPrivateKey_when_FileMissing_then_CreatesParentDirectory(t *testing.T) {
	// Given: a key path inside a directory that does not exist
	path := filepath.Join(t.TempDir(), "nested", "deeper", "jwt_private.pem")

	// When: the key is provisioned
	created, err := auth.EnsureRSAPrivateKey(path)

	// Then: the parents and the key file exist
	if err != nil {
		t.Fatalf("EnsureRSAPrivateKey: %v", err)
	}
	if !created {
		t.Error("created = false, want true when no key file exists")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("generated key file: %v", err)
	}
}

// Test_EnsureRSAPrivateKey_when_FileExists_then_KeepsIt pins that
// provisioning never overwrites operator-provisioned key material.
func Test_EnsureRSAPrivateKey_when_FileExists_then_KeepsIt(t *testing.T) {
	// Given: an existing operator-provisioned key
	key := generateRSAKey(t)
	path := writeKeyFile(t, key, true)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// When: provisioning runs
	created, err := auth.EnsureRSAPrivateKey(path)

	// Then: nothing was created and the file is byte-identical
	if err != nil {
		t.Fatalf("EnsureRSAPrivateKey: %v", err)
	}
	if created {
		t.Error("created = true, want false when a key file already exists")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("existing key file was modified")
	}
}
