package auth

import (
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
)

// ServeJWKS answers GET /v2/_keys with this signer's public key as a JWK Set
// (RFC 7517), so clients can verify our tokens without an out-of-band key
// exchange. The endpoint is public.
func (s *Signer) ServeJWKS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeTokenError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	// The JWK set is fixed-shape: marshalling cannot fail.
	body, _ := json.Marshal(jwkSet{Keys: []jwk{{
		Kty: "RSA",
		Kid: s.kid,
		Use: "sig",
		Alg: "RS256",
		N:   base64.RawURLEncoding.EncodeToString(s.key.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(s.key.E)).Bytes()),
	}}})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// jwkSet is the wire shape of GET /v2/_keys.
type jwkSet struct {
	Keys []jwk `json:"keys"`
}

// jwk is one RSA verification key (RFC 7517/7518: n and e are base64url of
// the big-endian integers, without padding).
type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}
