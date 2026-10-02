package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/user/better-docker-registry/internal/config"
)

// Claim-set vocabulary of the OCI token auth spec.
const (
	accessTypeRepository = "repository"
	accessActionAny      = "*"
)

// AccessEntry is one element of the JWT "access" claim: an authorisation on
// one named resource of the service.
type AccessEntry struct {
	Type    string   `json:"type"`
	Name    string   `json:"name"`
	Actions []string `json:"actions"`
}

// Claims is the registry token claim set (OCI token auth spec): the seven
// registered claims plus the private "access" array. aud is a plain string so
// the wire shape matches the spec's example claim set exactly.
type Claims struct {
	Issuer    string           `json:"iss"`
	Subject   string           `json:"sub"`
	Audience  string           `json:"aud"`
	ExpiresAt *jwt.NumericDate `json:"exp"`
	NotBefore *jwt.NumericDate `json:"nbf"`
	IssuedAt  *jwt.NumericDate `json:"iat"`
	ID        string           `json:"jti"`
	Access    []AccessEntry    `json:"access"`
}

// The six getters below make Claims a jwt.Claims; golang-jwt validates exp,
// nbf and the WithIssuer/WithAudience options through them.

func (c Claims) GetExpirationTime() (*jwt.NumericDate, error) { return c.ExpiresAt, nil }
func (c Claims) GetIssuedAt() (*jwt.NumericDate, error)      { return c.IssuedAt, nil }
func (c Claims) GetNotBefore() (*jwt.NumericDate, error)     { return c.NotBefore, nil }
func (c Claims) GetIssuer() (string, error)                  { return c.Issuer, nil }
func (c Claims) GetSubject() (string, error)                 { return c.Subject, nil }

func (c Claims) GetAudience() (jwt.ClaimStrings, error) {
	if c.Audience == "" {
		return nil, nil
	}
	return jwt.ClaimStrings{c.Audience}, nil
}

// HasAccess reports whether the claim set grants action on the named
// repository. The "*" action grants every action; entries of any other type
// than "repository" never grant repository access.
func (c Claims) HasAccess(name, action string) bool {
	for _, entry := range c.Access {
		if entry.Type != accessTypeRepository || entry.Name != name {
			continue
		}
		for _, granted := range entry.Actions {
			if granted == action || granted == accessActionAny {
				return true
			}
		}
	}
	return false
}

// Signer issues and validates registry tokens with one RSA key (RS256).
type Signer struct {
	cfg config.JWTConfig
	key *rsa.PrivateKey
	kid string
}

// LoadSigner reads the PEM private key named by cfg (PKCS#8 or PKCS#1 RSA)
// and returns a signer for that key. The key never appears in configuration
// values or logs: only its path does.
func LoadSigner(cfg config.JWTConfig) (*Signer, error) {
	if cfg.TokenTTL.Duration() <= 0 {
		return nil, errors.New("auth.jwt.token_ttl must be positive")
	}
	key, err := loadRSAPrivateKey(cfg.PrivateKeyPath)
	if err != nil {
		return nil, err
	}
	kid, err := keyID(&key.PublicKey)
	if err != nil {
		return nil, err
	}
	return &Signer{cfg: cfg, key: key, kid: kid}, nil
}

// loadRSAPrivateKey reads one PEM file holding an RSA private key. ECDSA keys
// are refused: this registry's token contract is RS256.
func loadRSAPrivateKey(path string) (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read jwt private key: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("jwt private key %s: no PEM block found", path)
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("jwt private key %s: %w", path, err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("jwt private key %s: not an RSA key", path)
	}
	return key, nil
}

// keyID derives the JOSE kid from the public key, so it is stable across
// processes and matches between tokens and the published key set.
func keyID(pub *rsa.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", fmt.Errorf("marshal jwt public key: %w", err)
	}
	sum := sha256.Sum256(der)
	return base64.RawURLEncoding.EncodeToString(sum[:8]), nil
}

// NewClaims builds the claim set for subject: issuer, audience and lifetime
// from the configuration, a fresh jti per call, and the requested access.
func (s *Signer) NewClaims(subject string, access []AccessEntry) *Claims {
	now := time.Now()
	if access == nil {
		access = []AccessEntry{}
	}
	return &Claims{
		Issuer:    s.cfg.Issuer,
		Subject:   subject,
		Audience:  s.cfg.Service,
		ExpiresAt: jwt.NewNumericDate(now.Add(s.cfg.TokenTTL.Duration())),
		NotBefore: jwt.NewNumericDate(now),
		IssuedAt:  jwt.NewNumericDate(now),
		ID:        rand.Text(),
		Access:    access,
	}
}

// Sign signs claims as an RS256 JWT stamped with this signer's kid.
func (s *Signer) Sign(claims *Claims) (string, error) {
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = s.kid
	signed, err := tok.SignedString(s.key)
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}
	return signed, nil
}

// Validate verifies a token and returns its claims. Acceptance rules: RS256
// over this signer's key, a kid naming that key when present, the configured
// issuer and audience, and an exp claim. golang-jwt's error chain keeps
// jwt.ErrTokenExpired and friends matchable with errors.Is.
func (s *Signer) Validate(tokenString string) (*Claims, error) {
	claims := &Claims{}
	if _, err := jwt.ParseWithClaims(tokenString, claims, s.keyFunc,
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithIssuer(s.cfg.Issuer),
		jwt.WithAudience(s.cfg.Service),
		jwt.WithExpirationRequired(),
	); err != nil {
		return nil, err
	}
	return claims, nil
}

// keyFunc returns the verification key, refusing tokens whose JOSE kid names
// a key other than this signer's (token spec: "verify that the provided KeyID
// is known").
func (s *Signer) keyFunc(tok *jwt.Token) (any, error) {
	if kid, ok := tok.Header["kid"].(string); ok && kid != s.kid {
		return nil, fmt.Errorf("unknown key id %q", kid)
	}
	return &s.key.PublicKey, nil
}
