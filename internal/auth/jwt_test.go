package auth_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/BRAVO68WEB/wharf/internal/auth"
)

// Test_NewClaims_when_SubjectAndAccess_then_FillsRegisteredClaims pins the
// claim set the token contract (OCI token auth spec) requires: iss, sub, aud,
// exp, nbf, iat, jti and the access array.
func Test_NewClaims_when_SubjectAndAccess_then_FillsRegisteredClaims(t *testing.T) {
	// Given: a signer configured with issuer, service and TTL
	env := newJWTEnv(t)
	access := []auth.AccessEntry{{
		Type: "repository", Name: "samalba/my-app", Actions: []string{"pull", "push"},
	}}

	// When: claims are built for a subject
	before := time.Now()
	claims := env.signer.NewClaims("jlhawn", access)
	after := time.Now()

	// Then: every registered claim is filled from the config and clock
	if claims.Issuer != jwtTestIssuer {
		t.Errorf("iss = %q, want %q", claims.Issuer, jwtTestIssuer)
	}
	if claims.Subject != "jlhawn" {
		t.Errorf("sub = %q, want %q", claims.Subject, "jlhawn")
	}
	if claims.Audience != jwtTestService {
		t.Errorf("aud = %q, want %q", claims.Audience, jwtTestService)
	}
	if claims.ExpiresAt == nil || claims.NotBefore == nil || claims.IssuedAt == nil {
		t.Fatalf("exp/nbf/iat must all be set, got exp=%v nbf=%v iat=%v",
			claims.ExpiresAt, claims.NotBefore, claims.IssuedAt)
	}
	// NumericDate truncates to whole seconds, hence the 2s window.
	if delta := claims.ExpiresAt.Sub(before); delta < jwtTestTTL-2*time.Second || delta > jwtTestTTL+2*time.Second {
		t.Errorf("exp = %v, want ~now+%v", claims.ExpiresAt.Time, jwtTestTTL)
	}
	if !claims.IssuedAt.Equal(claims.NotBefore.Time) {
		t.Errorf("nbf = %v, want iat %v", claims.NotBefore.Time, claims.IssuedAt.Time)
	}
	if got := claims.IssuedAt.Time; got.Before(before.Truncate(time.Second)) || got.After(after) {
		t.Errorf("iat = %v, want ~now", got)
	}
	if claims.ID == "" {
		t.Error("jti must be set")
	}
	if len(claims.Access) != 1 {
		t.Fatalf("access = %+v, want one entry", claims.Access)
	}
	if got := claims.Access[0]; got.Type != access[0].Type || got.Name != access[0].Name ||
		strings.Join(got.Actions, ",") != strings.Join(access[0].Actions, ",") {
		t.Errorf("access[0] = %+v, want %+v", got, access[0])
	}
}

// Test_NewClaims_when_NoAccess_then_EmptyAccessArray pins that a scope-less
// token still carries "access":[] (never null) so clients see a stable shape.
func Test_NewClaims_when_NoAccess_then_EmptyAccessArray(t *testing.T) {
	// Given: a signer
	env := newJWTEnv(t)

	// When: claims are built with no access entries
	claims := env.signer.NewClaims("jlhawn", nil)

	// Then: the access slice is present and empty
	if claims.Access == nil {
		t.Fatal("access must be non-nil so it marshals as []")
	}
	if len(claims.Access) != 0 {
		t.Errorf("access = %+v, want empty", claims.Access)
	}
}

// Test_NewClaims_when_TwiceCalled_then_JTIIsUnique pins that jti cannot repeat
// (it is the replay key of the token contract).
func Test_NewClaims_when_TwiceCalled_then_JTIIsUnique(t *testing.T) {
	// Given: a signer
	env := newJWTEnv(t)

	// When: two claim sets are built
	a := env.signer.NewClaims("jlhawn", nil)
	b := env.signer.NewClaims("jlhawn", nil)

	// Then: the token ids differ
	if a.ID == b.ID {
		t.Errorf("jti repeats: %q", a.ID)
	}
}

// Test_Sign_when_ValidClaims_then_RoundTripsThroughValidate pins the happy
// sign/verify path the Bearer middleware will rely on.
func Test_Sign_when_ValidClaims_then_RoundTripsThroughValidate(t *testing.T) {
	// Given: a signer and claims for one repository scope
	env := newJWTEnv(t)
	claims := env.signer.NewClaims("alice", []auth.AccessEntry{{
		Type: "repository", Name: "library/nginx", Actions: []string{"pull", "push"},
	}})

	// When: the claims are signed and validated back
	token, err := env.signer.Sign(claims)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	got, err := env.signer.Validate(token)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}

	// Then: the round-tripped claims match what was signed
	if got.Subject != claims.Subject {
		t.Errorf("sub = %q, want %q", got.Subject, claims.Subject)
	}
	if got.ID != claims.ID {
		t.Errorf("jti = %q, want %q", got.ID, claims.ID)
	}
	if len(got.Access) != 1 || got.Access[0].Name != "library/nginx" {
		t.Errorf("access = %+v, want the signed scope", got.Access)
	}
}

// Test_SignedToken_when_Decoded_then_ClaimWireShapeMatchesSpec pins the raw
// JSON shape: aud is a string, exp/nbf/iat are integer seconds, and the JOSE
// header carries typ JWT, alg RS256 and a kid.
func Test_SignedToken_when_Decoded_then_ClaimWireShapeMatchesSpec(t *testing.T) {
	// Given: a signed token
	env := newJWTEnv(t)
	token, err := env.signer.Sign(env.signer.NewClaims("alice", []auth.AccessEntry{{
		Type: "repository", Name: "samalba/my-app", Actions: []string{"pull", "push"},
	}}))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// When: the raw header and payload segments are decoded
	header := decodeHeader(t, token)
	payload := decodeClaims(t, token)

	// Then: the header is the JOSE header the spec requires
	if got := header["alg"]; got != "RS256" {
		t.Errorf("alg = %v, want RS256", got)
	}
	if got := header["typ"]; got != "JWT" {
		t.Errorf("typ = %v, want JWT", got)
	}
	if kid, ok := header["kid"].(string); !ok || kid == "" {
		t.Errorf("kid = %v, want a non-empty string", header["kid"])
	}

	// And: the claim set uses the spec's names and types
	if got := payload["iss"]; got != jwtTestIssuer {
		t.Errorf("iss = %v, want %q", got, jwtTestIssuer)
	}
	if got := payload["sub"]; got != "alice" {
		t.Errorf("sub = %v, want %q", got, "alice")
	}
	aud, ok := payload["aud"].(string)
	if !ok {
		t.Fatalf("aud = %v (%T), want a string", payload["aud"], payload["aud"])
	}
	if aud != jwtTestService {
		t.Errorf("aud = %q, want %q", aud, jwtTestService)
	}
	for _, field := range []string{"exp", "nbf", "iat"} {
		if _, ok := payload[field].(float64); !ok {
			t.Errorf("%s = %v (%T), want a number", field, payload[field], payload[field])
		}
	}
	exp, nbf, iat := payload["exp"].(float64), payload["nbf"].(float64), payload["iat"].(float64)
	if exp-iat != jwtTestTTL.Seconds() {
		t.Errorf("exp-iat = %v, want %v", exp-iat, jwtTestTTL.Seconds())
	}
	if nbf != iat {
		t.Errorf("nbf = %v, want iat %v", nbf, iat)
	}
	if jti, ok := payload["jti"].(string); !ok || jti == "" {
		t.Errorf("jti = %v, want a non-empty string", payload["jti"])
	}
	access, ok := payload["access"].([]any)
	if !ok || len(access) != 1 {
		t.Fatalf("access = %v, want one entry", payload["access"])
	}
	entry, ok := access[0].(map[string]any)
	if !ok {
		t.Fatalf("access[0] = %T, want an object", access[0])
	}
	if entry["type"] != "repository" || entry["name"] != "samalba/my-app" {
		t.Errorf("access entry = %v, want repository samalba/my-app", entry)
	}
	actions, ok := entry["actions"].([]any)
	if !ok || len(actions) != 2 || actions[0] != "pull" || actions[1] != "push" {
		t.Errorf("actions = %v, want [pull push]", entry["actions"])
	}
}

// Test_Validate_when_TokenExpired_then_Rejected is the acceptance criterion
// "expired JWT rejected".
func Test_Validate_when_TokenExpired_then_Rejected(t *testing.T) {
	// Given: a token whose expiry is in the past
	env := newJWTEnv(t)
	claims := env.signer.NewClaims("alice", nil)
	claims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Hour))
	claims.NotBefore = jwt.NewNumericDate(time.Now().Add(-2 * time.Hour))
	claims.IssuedAt = claims.NotBefore
	token, err := env.signer.Sign(claims)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// When: the token is validated
	_, err = env.signer.Validate(token)

	// Then: it is rejected as expired
	if err == nil {
		t.Fatal("Validate must reject an expired token")
	}
	if !errors.Is(err, jwt.ErrTokenExpired) {
		t.Errorf("error = %v, want errors.Is(err, jwt.ErrTokenExpired)", err)
	}
}

// Test_Validate_when_NotBeforeInFuture_then_Rejected pins the nbf bound.
func Test_Validate_when_NotBeforeInFuture_then_Rejected(t *testing.T) {
	// Given: a token that must not be used yet
	env := newJWTEnv(t)
	claims := env.signer.NewClaims("alice", nil)
	claims.NotBefore = jwt.NewNumericDate(time.Now().Add(time.Hour))
	token, err := env.signer.Sign(claims)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// When: the token is validated
	_, err = env.signer.Validate(token)

	// Then: it is rejected as not valid yet
	if !errors.Is(err, jwt.ErrTokenNotValidYet) {
		t.Errorf("error = %v, want errors.Is(err, jwt.ErrTokenNotValidYet)", err)
	}
}
