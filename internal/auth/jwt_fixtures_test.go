package auth_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BRAVO68WEB/wharf/internal/bcrypt"

	"github.com/BRAVO68WEB/wharf/internal/auth"
	"github.com/BRAVO68WEB/wharf/internal/config"
	"github.com/BRAVO68WEB/wharf/internal/db"
)

// Test fixtures for the auth package. Free helper names here are JWT-flavoured
// on purpose: task 12 adds session_test.go to this same test package and must
// be able to define its own bcrypt/DB helpers without a redeclaration clash.

const (
	jwtTestIssuer  = "auth.test.example"
	jwtTestService = "registry.test.example"
)

var jwtTestTTL = 15 * time.Minute

// jwtEnv is the fixture: a signer over a generated RSA key, the token
// endpoint wired to a real migrated SQLite database, and the key itself so
// tests can mint deliberately malformed tokens.
type jwtEnv struct {
	t       *testing.T
	cfg     config.JWTConfig
	key     *rsa.PrivateKey
	keyPath string
	queries db.Querier
	signer  *auth.Signer
	handler *auth.TokenHandler
}

// newJWTEnv builds the fixture around a freshly generated RSA key and a
// per-test database (real SQLite, not a mock — the users table is the
// contract under test).
func newJWTEnv(t *testing.T) *jwtEnv {
	t.Helper()
	key := generateRSAKey(t)
	keyPath := writeKeyFile(t, key, true)
	cfg := config.JWTConfig{
		PrivateKeyPath: keyPath,
		Issuer:         jwtTestIssuer,
		Service:        jwtTestService,
		TokenTTL:       config.Duration(jwtTestTTL),
	}
	signer, err := auth.LoadSigner(cfg)
	if err != nil {
		t.Fatalf("LoadSigner: %v", err)
	}
	sqlDB, err := db.OpenAndMigrate(t.Context(), filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	queries := db.New(sqlDB)
	return &jwtEnv{
		t:       t,
		cfg:     cfg,
		key:     key,
		keyPath: keyPath,
		queries: queries,
		signer:  signer,
		handler: auth.NewTokenHandler(queries, signer),
	}
}

// addUser stores a user with a bcrypt password hash, as the token endpoint
// expects it. bcrypt.MinCost keeps the suite fast; hash cost is orthogonal to
// the behaviour under test.
func (e *jwtEnv) addUser(username, password string) {
	e.t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		e.t.Fatalf("bcrypt hash: %v", err)
	}
	if _, err := e.queries.CreateUser(e.t.Context(), db.CreateUserParams{
		Username:     username,
		PasswordHash: string(hash),
		IsAdmin:      false,
	}); err != nil {
		e.t.Fatalf("CreateUser(%s): %v", username, err)
	}
}

// request sends one request to the token endpoint. A zero basic value means
// no Authorization header.
func (e *jwtEnv) request(method, target string, basic [2]string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if basic != [2]string{} {
		req.SetBasicAuth(basic[0], basic[1])
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	return rec
}

// requestKeys sends one request to the public key endpoint.
func (e *jwtEnv) requestKeys(method string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(method, "/v2/_keys", nil)
	rec := httptest.NewRecorder()
	e.signer.ServeJWKS(rec, req)
	return rec
}

func generateRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	return key
}

// writeKeyFile writes key as PEM: PKCS#8 when pkcs8, else PKCS#1.
func writeKeyFile(t *testing.T, key *rsa.PrivateKey, pkcs8 bool) string {
	t.Helper()
	var der []byte
	var err error
	if pkcs8 {
		der, err = x509.MarshalPKCS8PrivateKey(key)
	} else {
		der = x509.MarshalPKCS1PrivateKey(key)
	}
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	blockType := "RSA PRIVATE KEY"
	if pkcs8 {
		blockType = "PRIVATE KEY"
	}
	path := filepath.Join(t.TempDir(), "jwt_private.pem")
	data := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	return path
}

// decodeSegment decodes one raw base64url JWT segment as generic JSON.
func decodeSegment(t *testing.T, token string, part int) map[string]any {
	t.Helper()
	segs := strings.Split(token, ".")
	if len(segs) != 3 {
		t.Fatalf("token has %d segments, want 3", len(segs))
	}
	raw, err := base64.RawURLEncoding.DecodeString(segs[part])
	if err != nil {
		t.Fatalf("decode segment %d: %v", part, err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal segment %d: %v", part, err)
	}
	return m
}

func decodeHeader(t *testing.T, token string) map[string]any {
	t.Helper()
	return decodeSegment(t, token, 0)
}

func decodeClaims(t *testing.T, token string) map[string]any {
	t.Helper()
	return decodeSegment(t, token, 1)
}

// tokenBody is the token endpoint's success response.
type tokenBody struct {
	Token       string `json:"token"`
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
	IssuedAt    string `json:"issued_at"`
}

func decodeTokenBody(t *testing.T, rec *httptest.ResponseRecorder) tokenBody {
	t.Helper()
	var body tokenBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode token response: %v (body %q)", err, rec.Body.String())
	}
	return body
}
