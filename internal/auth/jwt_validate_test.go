package auth_test

import (
	"crypto/x509"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

// mint signs claims with the raw jwt library: the tests below need tokens the
// Signer would never produce (wrong key, wrong alg, wrong kid).
func mint(t *testing.T, method jwt.SigningMethod, claims jwt.Claims, key any) string {
	t.Helper()
	tok := jwt.NewWithClaims(method, claims)
	signed, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	return signed
}

// Test_Validate_when_SignatureTampered_then_Rejected is the acceptance
// criterion "tampered JWT rejected".
func Test_Validate_when_SignatureTampered_then_Rejected(t *testing.T) {
	// Given: a validly signed token
	env := newJWTEnv(t)
	token, err := env.signer.Sign(env.signer.NewClaims("alice", nil))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// When: one byte of the signature segment is flipped
	segs := strings.Split(token, ".")
	sig, err := base64.RawURLEncoding.DecodeString(segs[2])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	sig[len(sig)-1] ^= 0xff
	segs[2] = base64.RawURLEncoding.EncodeToString(sig)
	tampered := strings.Join(segs, ".")

	// Then: validation rejects it as an invalid signature
	_, err = env.signer.Validate(tampered)
	if !errors.Is(err, jwt.ErrTokenSignatureInvalid) {
		t.Errorf("error = %v, want errors.Is(err, jwt.ErrTokenSignatureInvalid)", err)
	}
}

// Test_Validate_when_PayloadTampered_then_Rejected pins that the signature
// covers the claims: editing the subject without the key is rejected.
func Test_Validate_when_PayloadTampered_then_Rejected(t *testing.T) {
	// Given: a validly signed token for subject alice
	env := newJWTEnv(t)
	token, err := env.signer.Sign(env.signer.NewClaims("alice", nil))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// When: the payload's subject is rewritten to mallory
	segs := strings.Split(token, ".")
	payload, err := base64.RawURLEncoding.DecodeString(segs[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	forged := strings.Replace(string(payload), `"sub":"alice"`, `"sub":"mallory"`, 1)
	if forged == string(payload) {
		t.Fatal("fixture must contain a sub claim to tamper with")
	}
	segs[1] = base64.RawURLEncoding.EncodeToString([]byte(forged))
	tampered := strings.Join(segs, ".")

	// Then: validation rejects it
	_, err = env.signer.Validate(tampered)
	if !errors.Is(err, jwt.ErrTokenSignatureInvalid) {
		t.Errorf("error = %v, want errors.Is(err, jwt.ErrTokenSignatureInvalid)", err)
	}
}

// Test_Validate_when_SignedByOtherKey_then_Rejected pins the key check: only
// tokens signed with the configured private key verify.
func Test_Validate_when_SignedByOtherKey_then_Rejected(t *testing.T) {
	// Given: a token signed with a different RSA key
	env := newJWTEnv(t)
	token := mint(t, jwt.SigningMethodRS256, env.signer.NewClaims("alice", nil), generateRSAKey(t))

	// When: it is validated against the configured signer
	_, err := env.signer.Validate(token)

	// Then: the signature does not verify
	if !errors.Is(err, jwt.ErrTokenSignatureInvalid) {
		t.Errorf("error = %v, want errors.Is(err, jwt.ErrTokenSignatureInvalid)", err)
	}
}

// Test_Validate_when_AlgorithmHS256_then_Rejected pins the alg-confusion
// defence: an HMAC token "signed" with the public key must not verify even
// though the HMAC would be self-consistent.
func Test_Validate_when_AlgorithmHS256_then_Rejected(t *testing.T) {
	// Given: an HS256 token keyed with the signer's public key bytes
	env := newJWTEnv(t)
	pub, err := x509.MarshalPKIXPublicKey(&env.key.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	token := mint(t, jwt.SigningMethodHS256, env.signer.NewClaims("alice", nil), pub)

	// When: it is validated
	_, err = env.signer.Validate(token)

	// Then: RS256 is the only accepted algorithm
	if !errors.Is(err, jwt.ErrTokenSignatureInvalid) {
		t.Errorf("error = %v, want errors.Is(err, jwt.ErrTokenSignatureInvalid)", err)
	}
}

// Test_Validate_when_AlgorithmNone_then_Rejected pins that unsigned tokens
// are refused outright.
func Test_Validate_when_AlgorithmNone_then_Rejected(t *testing.T) {
	// Given: a token with alg "none" and no signature
	env := newJWTEnv(t)
	token := mint(t, jwt.SigningMethodNone, env.signer.NewClaims("alice", nil), jwt.UnsafeAllowNoneSignatureType)

	// When: it is validated
	got, err := env.signer.Validate(token)

	// Then: it is rejected and no claims come back
	if err == nil {
		t.Fatal("Validate must reject an unsigned token")
	}
	if got != nil {
		t.Errorf("claims = %+v, want nil on rejection", got)
	}
}

// Test_Validate_when_WrongKid_then_Rejected pins that the JOSE kid must name
// the configured key (token spec: "verify that the provided KeyID is known").
func Test_Validate_when_WrongKid_then_Rejected(t *testing.T) {
	// Given: a correctly signed token whose header names another key
	env := newJWTEnv(t)
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, env.signer.NewClaims("alice", nil))
	tok.Header["kid"] = "some-other-key"
	token, err := tok.SignedString(env.key)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// When: it is validated
	_, err = env.signer.Validate(token)

	// Then: the key lookup fails before verification
	if !errors.Is(err, jwt.ErrTokenUnverifiable) {
		t.Errorf("error = %v, want errors.Is(err, jwt.ErrTokenUnverifiable)", err)
	}
}

// Test_Validate_when_IssuerMismatch_then_Rejected pins the iss check.
func Test_Validate_when_IssuerMismatch_then_Rejected(t *testing.T) {
	// Given: a token minted for another issuer
	env := newJWTEnv(t)
	claims := env.signer.NewClaims("alice", nil)
	claims.Issuer = "auth.attacker.example"
	token, err := env.signer.Sign(claims)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// When: it is validated
	_, err = env.signer.Validate(token)

	// Then: the issuer is not trusted
	if !errors.Is(err, jwt.ErrTokenInvalidIssuer) {
		t.Errorf("error = %v, want errors.Is(err, jwt.ErrTokenInvalidIssuer)", err)
	}
}

// Test_Validate_when_AudienceMismatch_then_Rejected pins the aud check: a
// token issued for another service cannot be replayed here.
func Test_Validate_when_AudienceMismatch_then_Rejected(t *testing.T) {
	// Given: a token minted for another audience
	env := newJWTEnv(t)
	claims := env.signer.NewClaims("alice", nil)
	claims.Audience = "registry.other.example"
	token, err := env.signer.Sign(claims)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// When: it is validated
	_, err = env.signer.Validate(token)

	// Then: the audience does not match the configured service
	if !errors.Is(err, jwt.ErrTokenInvalidAudience) {
		t.Errorf("error = %v, want errors.Is(err, jwt.ErrTokenInvalidAudience)", err)
	}
}

// Test_Validate_when_ClaimsNotForRegistry_then_Rejected pins that exp is
// mandatory: a token without expiry must never be accepted.
func Test_Validate_when_ClaimsNotForRegistry_then_Rejected(t *testing.T) {
	// Given: a signed token without an exp claim
	env := newJWTEnv(t)
	claims := env.signer.NewClaims("alice", nil)
	claims.ExpiresAt = nil
	token, err := env.signer.Sign(claims)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// When: it is validated
	_, err = env.signer.Validate(token)

	// Then: it is rejected because expiry is required
	if err == nil {
		t.Fatal("Validate must reject a token without exp")
	}
}
