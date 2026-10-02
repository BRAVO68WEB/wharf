package auth_test

import (
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"testing"
)

// jwksResponse is the wire shape of GET /v2/_keys (RFC 7517 JWK Set).
type jwksResponse struct {
	Keys []jwkResponse `json:"keys"`
}

type jwkResponse struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// Test_JWKS_when_Requested_then_ReturnsConfiguredRSAKey pins the public key
// endpoint: a client must be able to verify our tokens from it alone.
func Test_JWKS_when_Requested_then_ReturnsConfiguredRSAKey(t *testing.T) {
	// Given: the fixture signer
	env := newJWTEnv(t)

	// When: the keys endpoint is queried
	rec := env.requestKeys(http.MethodGet)

	// Then: it answers 200 with a one-key JWK Set
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var set jwksResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &set); err != nil {
		t.Fatalf("decode JWKS: %v (body %q)", err, rec.Body.String())
	}
	if len(set.Keys) != 1 {
		t.Fatalf("keys = %d, want 1 (body %q)", len(set.Keys), rec.Body.String())
	}
	jwk := set.Keys[0]

	// And: the JWK describes the configured RSA key
	if jwk.Kty != "RSA" || jwk.Alg != "RS256" || jwk.Use != "sig" {
		t.Errorf("jwk = %+v, want kty RSA, alg RS256, use sig", jwk)
	}
	if jwk.Kid == "" {
		t.Error("kid must be set")
	}
	n, err := base64.RawURLEncoding.DecodeString(jwk.N)
	if err != nil {
		t.Fatalf("decode n: %v", err)
	}
	if new(big.Int).SetBytes(n).Cmp(env.key.N) != 0 {
		t.Error("n does not match the configured key's modulus")
	}
	e, err := base64.RawURLEncoding.DecodeString(jwk.E)
	if err != nil {
		t.Fatalf("decode e: %v", err)
	}
	if int(new(big.Int).SetBytes(e).Int64()) != env.key.E {
		t.Errorf("e = %x, want %x", e, env.key.E)
	}
}

// Test_JWKS_when_Kid_then_MatchesTokenHeader pins that a client can select
// the key by the JOSE kid it finds in tokens.
func Test_JWKS_when_Kid_then_MatchesTokenHeader(t *testing.T) {
	// Given: a signed token and the published key set
	env := newJWTEnv(t)
	token, err := env.signer.Sign(env.signer.NewClaims("alice", nil))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	rec := env.requestKeys(http.MethodGet)
	var set jwksResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &set); err != nil {
		t.Fatalf("decode JWKS: %v", err)
	}

	// Then: the header kid names the published key
	if got := decodeHeader(t, token)["kid"]; got != set.Keys[0].Kid {
		t.Errorf("header kid = %v, jwk kid = %v, want equal", got, set.Keys[0].Kid)
	}
}

// Test_JWKS_when_MethodNotAllowed_then_405 pins that only GET is served.
func Test_JWKS_when_MethodNotAllowed_then_405(t *testing.T) {
	// Given: the fixture signer
	env := newJWTEnv(t)

	// When: the keys endpoint is addressed with POST
	rec := env.requestKeys(http.MethodPost)

	// Then: it is refused
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow != http.MethodGet {
		t.Errorf("Allow = %q, want GET", allow)
	}
}
