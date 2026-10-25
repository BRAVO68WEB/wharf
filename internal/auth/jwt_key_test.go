package auth_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BRAVO68WEB/wharf/internal/auth"
	"github.com/BRAVO68WEB/wharf/internal/config"
)

// Test_LoadSigner_when_PKCS8AndPKCS1_then_BothAccepted pins that either
// common PEM encoding of an RSA private key works.
func Test_LoadSigner_when_PKCS8AndPKCS1_then_BothAccepted(t *testing.T) {
	// Given: the same key encoded as PKCS#8 and PKCS#1
	key := generateRSAKey(t)
	pkcs8Path := writeKeyFile(t, key, true)
	pkcs1Path := writeKeyFile(t, key, false)

	// When: both files are loaded
	cfg8 := config.JWTConfig{PrivateKeyPath: pkcs8Path, Issuer: jwtTestIssuer, Service: jwtTestService, TokenTTL: config.Duration(jwtTestTTL)}
	cfg1 := config.JWTConfig{PrivateKeyPath: pkcs1Path, Issuer: jwtTestIssuer, Service: jwtTestService, TokenTTL: config.Duration(jwtTestTTL)}
	s8, err := auth.LoadSigner(cfg8)
	if err != nil {
		t.Fatalf("LoadSigner(PKCS#8): %v", err)
	}
	s1, err := auth.LoadSigner(cfg1)
	if err != nil {
		t.Fatalf("LoadSigner(PKCS#1): %v", err)
	}

	// Then: both sign tokens the other one accepts (same key material)
	tok, err := s8.Sign(s8.NewClaims("alice", nil))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := s1.Validate(tok); err != nil {
		t.Errorf("cross-validate: %v", err)
	}
}

// Test_LoadSigner_when_Kid_then_StableAcrossLoads pins that the key id is a
// function of the key (so clients can cache the JWKS) and matches the JOSE
// header.
func Test_LoadSigner_when_Kid_then_StableAcrossLoads(t *testing.T) {
	// Given: one key file loaded twice
	env := newJWTEnv(t)
	again, err := auth.LoadSigner(env.cfg)
	if err != nil {
		t.Fatalf("LoadSigner: %v", err)
	}

	// When: both sign a token
	a, err := env.signer.Sign(env.signer.NewClaims("alice", nil))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	b, err := again.Sign(again.NewClaims("alice", nil))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// Then: the JOSE kid is identical
	kidA := decodeHeader(t, a)["kid"]
	kidB := decodeHeader(t, b)["kid"]
	if kidA != kidB {
		t.Errorf("kid = %v then %v, want stable", kidA, kidB)
	}
	if kidA == "" {
		t.Error("kid must not be empty")
	}
}

// Test_LoadSigner_when_FileMissing_then_Error pins the fail-fast startup
// behaviour for a bad private_key_path.
func Test_LoadSigner_when_FileMissing_then_Error(t *testing.T) {
	// Given: a config naming a file that does not exist
	cfg := config.JWTConfig{
		PrivateKeyPath: filepath.Join(t.TempDir(), "nope.pem"),
		Issuer:         jwtTestIssuer,
		Service:        jwtTestService,
		TokenTTL:       config.Duration(jwtTestTTL),
	}

	// When: the signer is loaded
	_, err := auth.LoadSigner(cfg)

	// Then: it fails
	if err == nil {
		t.Fatal("LoadSigner must fail for a missing key file")
	}
}

// Test_LoadSigner_when_GarbagePEM_then_Error pins that non-key PEM content is
// refused rather than silently producing an unusable signer.
func Test_LoadSigner_when_GarbagePEM_then_Error(t *testing.T) {
	// Given: a PEM file that is not a private key
	path := filepath.Join(t.TempDir(), "jwt_private.pem")
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("not a key")})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.JWTConfig{PrivateKeyPath: path, Issuer: jwtTestIssuer, Service: jwtTestService, TokenTTL: config.Duration(jwtTestTTL)}

	// When: the signer is loaded
	_, err := auth.LoadSigner(cfg)

	// Then: it fails
	if err == nil {
		t.Fatal("LoadSigner must fail for a PEM block that is not a private key")
	}
}

// Test_LoadSigner_when_ECKey_then_Rejected pins the RS256-only boundary: the
// token contract of this registry is RSA.
func Test_LoadSigner_when_ECKey_then_Rejected(t *testing.T) {
	// Given: a PKCS#8 PEM holding an ECDSA key
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(ecKey)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "jwt_private.pem")
	data := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.JWTConfig{PrivateKeyPath: path, Issuer: jwtTestIssuer, Service: jwtTestService, TokenTTL: config.Duration(jwtTestTTL)}

	// When: the signer is loaded
	_, err = auth.LoadSigner(cfg)

	// Then: it fails with an RSA-specific message
	if err == nil {
		t.Fatal("LoadSigner must reject a non-RSA key")
	}
	if !strings.Contains(err.Error(), "RSA") {
		t.Errorf("error = %v, want it to mention RSA", err)
	}
}

// Test_LoadSigner_when_TTLNotPositive_then_Error pins the constructor
// precondition: a zero TTL would mint tokens that are expired on arrival.
func Test_LoadSigner_when_TTLNotPositive_then_Error(t *testing.T) {
	// Given: a valid key file but a zero TTL
	keyPath := writeKeyFile(t, generateRSAKey(t), true)
	cfg := config.JWTConfig{PrivateKeyPath: keyPath, Issuer: jwtTestIssuer, Service: jwtTestService}

	// When: the signer is loaded
	_, err := auth.LoadSigner(cfg)

	// Then: it fails
	if err == nil {
		t.Fatal("LoadSigner must reject a non-positive token_ttl")
	}
}
