package middleware_test

// Self-contained on purpose: the plan's acceptance runs this file alone in
// file-list mode (`go test ./internal/middleware/auth_test.go`), so no helper
// from the sibling test files may be referenced here (task 12 precedent).

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/user/better-docker-registry/internal/auth"
	"github.com/user/better-docker-registry/internal/config"
	"github.com/user/better-docker-registry/internal/middleware"
)

const (
	bearerService = "test-service"
	bearerIssuer  = "test-issuer"
	bearerTarget  = "http://registry.test/v2/app/manifests/latest"

	bearerChallenge = `Bearer realm="http://registry.test/token",service="test-service",scope=""`
	bearerRequired  = `{"errors":[{"code":"UNAUTHORIZED","message":"authentication required","detail":{}}]}`
	bearerInvalid   = `{"errors":[{"code":"UNAUTHORIZED","message":"invalid bearer token","detail":{}}]}`
	bearerDenied    = `{"errors":[{"code":"DENIED","message":"requested access to the resource is denied","detail":{}}]}`
	bearerAPIHeader = "Docker-Distribution-API-Version"
	bearerAPIValue  = "registry/2.0"
)

// bearerSigner builds a real RS256 signer over a throwaway key file.
func bearerSigner(t *testing.T) *auth.Signer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	path := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	signer, err := auth.LoadSigner(config.JWTConfig{
		PrivateKeyPath: path,
		Issuer:         bearerIssuer,
		Service:        bearerService,
		TokenTTL:       config.Duration(time.Hour),
	})
	if err != nil {
		t.Fatalf("LoadSigner: %v", err)
	}
	return signer
}

// bearerToken signs a real token for subject with the given access entries.
func bearerToken(t *testing.T, s *auth.Signer, subject string, access ...auth.AccessEntry) string {
	t.Helper()
	tok, err := s.Sign(s.NewClaims(subject, access))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return tok
}

func bearerHandler(signer *auth.Signer, next http.Handler) http.Handler {
	return middleware.BearerAuth(signer, bearerService, "")(next)
}

// bearerProbe answers 200 and records whether the chain reached the handler.
func bearerProbe(called *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*called = true
		w.WriteHeader(http.StatusOK)
	})
}

// bearerDo runs one request. authorization is the raw header value ("" omits it).
func bearerDo(t *testing.T, h http.Handler, method, target, authorization string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func bearerAssert(t *testing.T, rec *httptest.ResponseRecorder, status int, body string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, status, rec.Body.String())
	}
	if body != "" && rec.Body.String() != body {
		t.Fatalf("body = %s, want %s", rec.Body.String(), body)
	}
}

func Test_BearerAuth_when_MissingOrMalformedAuthorization_then_401Challenge(t *testing.T) {
	signer := bearerSigner(t)
	for _, header := range []string{"", "Basic dXNlcjpwYXNz", "Bearer", "Token abc", "bearer x"} {
		called := false
		rec := bearerDo(t, bearerHandler(signer, bearerProbe(&called)), http.MethodGet, bearerTarget, header)
		bearerAssert(t, rec, http.StatusUnauthorized, bearerRequired)
		if got := rec.Header().Get("WWW-Authenticate"); got != bearerChallenge {
			t.Fatalf("WWW-Authenticate = %q, want %q", got, bearerChallenge)
		}
		if called {
			t.Fatalf("handler ran for header %q", header)
		}
	}
}

func Test_BearerAuth_when_RealmConfigured_then_ChallengeIgnoresHost(t *testing.T) {
	signer := bearerSigner(t)
	called := false
	h := middleware.BearerAuth(signer, bearerService, "https://registry.example.com")(bearerProbe(&called))
	rec := bearerDo(t, h, http.MethodGet, "http://evil.example/v2/", "")
	bearerAssert(t, rec, http.StatusUnauthorized, bearerRequired)
	want := `Bearer realm="https://registry.example.com/token",service="test-service",scope=""`
	if got := rec.Header().Get("WWW-Authenticate"); got != want {
		t.Fatalf("WWW-Authenticate = %q, want %q", got, want)
	}
	if called {
		t.Fatal("handler ran without a token")
	}
}

func Test_BearerAuth_when_InvalidToken_then_401(t *testing.T) {
	signer := bearerSigner(t)
	expired := signer.NewClaims("alice", nil)
	expired.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute))
	expiredTok, err := signer.Sign(expired)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	wrongIssuer := signer.NewClaims("alice", nil)
	wrongIssuer.Issuer = "evil"
	wrongIssuerTok, err := signer.Sign(wrongIssuer)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	foreign := bearerToken(t, bearerSigner(t), "alice")
	cases := map[string]string{
		"garbage":       "garbage",
		"empty":         "",
		"expired":       expiredTok,
		"wrong issuer":  wrongIssuerTok,
		"foreign key":   foreign,
		"tampered body": expiredTok[:len(expiredTok)-2] + "aa",
	}
	for name, tok := range cases {
		called := false
		rec := bearerDo(t, bearerHandler(signer, bearerProbe(&called)), http.MethodGet, bearerTarget, "Bearer "+tok)
		bearerAssert(t, rec, http.StatusUnauthorized, bearerInvalid)
		if called {
			t.Fatalf("handler ran for case %q", name)
		}
	}
}

func Test_BearerAuth_when_ValidToken_then_UserAndScopesInContext(t *testing.T) {
	var user string
	var scopes []auth.AccessEntry
	var okUser, okScopes bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, okUser = middleware.AuthUser(r.Context())
		scopes, okScopes = middleware.AuthScopes(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	signer := bearerSigner(t)
	tok := bearerToken(t, signer, "alice", auth.AccessEntry{Type: "repository", Name: "app", Actions: []string{"pull"}})
	rec := bearerDo(t, bearerHandler(signer, next), http.MethodGet, bearerTarget, "Bearer "+tok)
	bearerAssert(t, rec, http.StatusOK, "")
	if !okUser || user != "alice" {
		t.Fatalf("AuthUser = %q,%v want alice,true", user, okUser)
	}
	if !okScopes || len(scopes) != 1 || scopes[0].Name != "app" {
		t.Fatalf("AuthScopes = %v,%v want one app entry", scopes, okScopes)
	}
	if _, ok := middleware.AuthUser(t.Context()); ok {
		t.Fatal("AuthUser must be absent on an unauthenticated context")
	}
	if _, ok := middleware.AuthScopes(t.Context()); ok {
		t.Fatal("AuthScopes must be absent on an unauthenticated context")
	}
}

func Test_BearerAuth_when_MethodScopeMatrix_then_Enforced(t *testing.T) {
	signer := bearerSigner(t)
	entry := func(actions ...string) []auth.AccessEntry {
		return []auth.AccessEntry{{Type: "repository", Name: "app", Actions: actions}}
	}
	cases := []struct {
		name   string
		method string
		access []auth.AccessEntry
		want   int
	}{
		{"pull get", http.MethodGet, entry("pull"), http.StatusOK},
		{"pull head", http.MethodHead, entry("pull"), http.StatusOK},
		{"pull put", http.MethodPut, entry("pull"), http.StatusForbidden},
		{"pull patch", http.MethodPatch, entry("pull"), http.StatusForbidden},
		{"pull delete", http.MethodDelete, entry("pull"), http.StatusForbidden},
		{"push put", http.MethodPut, entry("push"), http.StatusOK},
		{"push patch", http.MethodPatch, entry("push"), http.StatusOK},
		{"push post", http.MethodPost, entry("push"), http.StatusOK},
		{"push delete", http.MethodDelete, entry("push"), http.StatusOK},
		{"push does not grant pull", http.MethodGet, entry("push"), http.StatusForbidden},
		{"wildcard get", http.MethodGet, entry("*"), http.StatusOK},
		{"wildcard put", http.MethodPut, entry("*"), http.StatusOK},
		{"wrong repository", http.MethodGet, []auth.AccessEntry{{Type: "repository", Name: "other", Actions: []string{"*"}}}, http.StatusForbidden},
		{"wrong type", http.MethodGet, []auth.AccessEntry{{Type: "registry", Name: "app", Actions: []string{"*"}}}, http.StatusForbidden},
		{"empty access", http.MethodGet, nil, http.StatusForbidden},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			called := false
			tok := bearerToken(t, signer, "alice", c.access...)
			rec := bearerDo(t, bearerHandler(signer, bearerProbe(&called)), c.method, bearerTarget, "Bearer "+tok)
			bearerAssert(t, rec, c.want, "")
			if (rec.Code == http.StatusOK) != called {
				t.Fatalf("called = %v for status %d", called, rec.Code)
			}
		})
	}
}

func Test_BearerAuth_when_InsufficientScope_then_403DeniedWithScopeChallenge(t *testing.T) {
	called := false
	signer := bearerSigner(t)
	tok := bearerToken(t, signer, "alice", auth.AccessEntry{Type: "repository", Name: "app", Actions: []string{"pull"}})
	rec := bearerDo(t, bearerHandler(signer, bearerProbe(&called)), http.MethodPut, bearerTarget, "Bearer "+tok)
	bearerAssert(t, rec, http.StatusForbidden, bearerDenied)
	want := `Bearer realm="http://registry.test/token",service="test-service",scope="repository:app:push"`
	if got := rec.Header().Get("WWW-Authenticate"); got != want {
		t.Fatalf("WWW-Authenticate = %q, want %q", got, want)
	}
	if called {
		t.Fatal("handler must not run without scope")
	}
}

func Test_BearerAuth_when_SlashedRepoName_then_ScopedToFullName(t *testing.T) {
	signer := bearerSigner(t)
	target := "http://registry.test/v2/library/nginx/manifests/latest"
	called := false
	tok := bearerToken(t, signer, "alice", auth.AccessEntry{Type: "repository", Name: "library/nginx", Actions: []string{"pull"}})
	bearerAssert(t, bearerDo(t, bearerHandler(signer, bearerProbe(&called)), http.MethodGet, target, "Bearer "+tok), http.StatusOK, "")
	partial := bearerToken(t, signer, "alice", auth.AccessEntry{Type: "repository", Name: "nginx", Actions: []string{"pull"}})
	bearerAssert(t, bearerDo(t, bearerHandler(signer, bearerProbe(&called)), http.MethodGet, target, "Bearer "+partial), http.StatusForbidden, bearerDenied)

	blob := "http://registry.test/v2/team/blobs/app/blobs/sha256:abcd"
	full := bearerToken(t, signer, "alice", auth.AccessEntry{Type: "repository", Name: "team/blobs/app", Actions: []string{"pull"}})
	called = false
	bearerAssert(t, bearerDo(t, bearerHandler(signer, bearerProbe(&called)), http.MethodGet, blob, "Bearer "+full), http.StatusOK, "")
	if !called {
		t.Fatal("blob URL under team/blobs/app did not reach the handler")
	}
}

func Test_BearerAuth_when_VersionCheck_then_401Or200(t *testing.T) {
	signer := bearerSigner(t)
	for _, target := range []string{"http://registry.test/v2/", "http://registry.test/v2"} {
		called := false
		rec := bearerDo(t, bearerHandler(signer, bearerProbe(&called)), http.MethodGet, target, "")
		bearerAssert(t, rec, http.StatusUnauthorized, bearerRequired)
		tok := bearerToken(t, signer, "alice") // scope-less login probe token
		rec = bearerDo(t, bearerHandler(signer, bearerProbe(&called)), http.MethodGet, target, "Bearer "+tok)
		bearerAssert(t, rec, http.StatusOK, "")
		if called {
			t.Fatalf("version check on %s must be answered by the middleware", target)
		}
		if got := rec.Header().Get(bearerAPIHeader); got != bearerAPIValue {
			t.Fatalf("%s = %q, want %q", bearerAPIHeader, got, bearerAPIValue)
		}
	}
}

func Test_BearerAuth_when_AnonymousToken_then_VersionCheckOnly(t *testing.T) {
	signer := bearerSigner(t)
	tok := bearerToken(t, signer, auth.AnonymousSubject, auth.AccessEntry{
		Type: "repository", Name: "app", Actions: []string{"pull"},
	})
	called := false
	version := bearerDo(t, bearerHandler(signer, bearerProbe(&called)), http.MethodGet, "http://registry.test/v2/", "Bearer "+tok)
	bearerAssert(t, version, http.StatusOK, "")
	if called {
		t.Fatal("version check must be answered by the middleware")
	}
	catalog := bearerDo(t, bearerHandler(signer, bearerProbe(&called)), http.MethodGet, "http://registry.test/v2/_catalog", "Bearer "+tok)
	bearerAssert(t, catalog, http.StatusForbidden, bearerDenied)
	if called {
		t.Fatal("anonymous token must not reach the catalog")
	}
}

func Test_BearerAuth_when_TokenOrNonV2Path_then_NoAuthRequired(t *testing.T) {
	signer := bearerSigner(t)
	for _, target := range []string{"http://registry.test/token", "http://registry.test/dashboard", "http://registry.test/healthz"} {
		called := false
		rec := bearerDo(t, bearerHandler(signer, bearerProbe(&called)), http.MethodGet, target, "")
		bearerAssert(t, rec, http.StatusOK, "")
		if !called {
			t.Fatalf("%s must reach the handler unauthenticated", target)
		}
	}
}
