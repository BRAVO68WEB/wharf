package auth_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const (
	unauthorizedBody = `{"error":"invalid credentials"}`
	missingService   = `{"error":"service parameter is required"}`
	unknownService   = `{"error":"unknown service"}`
)

// Test_TokenEndpoint_when_ValidCreds_then_ReturnsJWTWithScope is the QA happy
// path: GET /token with valid Basic creds returns a JWT that decodes with the
// requested access scope.
func Test_TokenEndpoint_when_ValidCreds_then_ReturnsJWTWithScope(t *testing.T) {
	// Given: a registered user and a scoped token request
	env := newJWTEnv(t)
	env.addUser("alice", "s3cret")
	target := "/token?service=" + jwtTestService +
		"&scope=repository:library/nginx:pull,push"

	// When: the token endpoint is called with valid Basic credentials
	rec := env.request(http.MethodGet, target, [2]string{"alice", "s3cret"})

	// Then: it answers 200 with a JSON token response
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	body := decodeTokenBody(t, rec)
	if body.Token == "" {
		t.Fatal("token must be set")
	}
	if body.AccessToken != body.Token {
		t.Errorf("access_token = %q, want the same value as token", body.AccessToken)
	}
	if body.ExpiresIn != int64(jwtTestTTL.Seconds()) {
		t.Errorf("expires_in = %d, want %d", body.ExpiresIn, int64(jwtTestTTL.Seconds()))
	}
	issuedAt, err := time.Parse(time.RFC3339, body.IssuedAt)
	if err != nil {
		t.Fatalf("issued_at = %q, want RFC3339: %v", body.IssuedAt, err)
	}
	if delta := time.Since(issuedAt); delta < -time.Minute || delta > time.Minute {
		t.Errorf("issued_at = %v, want ~now", issuedAt)
	}

	// And: the JWT carries the claims the token contract requires
	payload := decodeClaims(t, body.Token)
	if payload["sub"] != "alice" {
		t.Errorf("sub = %v, want alice", payload["sub"])
	}
	if payload["iss"] != jwtTestIssuer {
		t.Errorf("iss = %v, want %q", payload["iss"], jwtTestIssuer)
	}
	if payload["aud"] != jwtTestService {
		t.Errorf("aud = %v, want %q", payload["aud"], jwtTestService)
	}
	access, _ := payload["access"].([]any)
	if len(access) != 1 {
		t.Fatalf("access = %v, want one entry", payload["access"])
	}
	entry, _ := access[0].(map[string]any)
	if entry["type"] != "repository" || entry["name"] != "library/nginx" {
		t.Errorf("entry = %v, want repository library/nginx", entry)
	}
	if fmt.Sprint(entry["actions"]) != "[pull push]" {
		t.Errorf("actions = %v, want [pull push]", entry["actions"])
	}

	// And: the issued token validates through the signer (acceptance: the
	// token round-trips)
	if _, err := env.signer.Validate(body.Token); err != nil {
		t.Errorf("Validate(issued token): %v", err)
	}
}

// Test_TokenEndpoint_when_BadPassword_then_401 is the QA failure path.
func Test_TokenEndpoint_when_BadPassword_then_401(t *testing.T) {
	// Given: a registered user
	env := newJWTEnv(t)
	env.addUser("alice", "s3cret")

	// When: the token endpoint is called with the wrong password
	rec := env.request(http.MethodGet, "/token?service="+jwtTestService,
		[2]string{"alice", "wrong"})

	// Then: it answers 401 with the Basic challenge and a fixed body
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if got, want := rec.Header().Get("WWW-Authenticate"), `Basic realm="`+jwtTestService+`"`; got != want {
		t.Errorf("WWW-Authenticate = %q, want %q", got, want)
	}
	if rec.Body.String() != unauthorizedBody {
		t.Errorf("body = %q, want %q", rec.Body.String(), unauthorizedBody)
	}
}

// Test_TokenEndpoint_when_UnknownUser_then_SameRejectionAsBadPassword pins
// that the response does not reveal whether the account exists.
func Test_TokenEndpoint_when_UnknownUser_then_SameRejectionAsBadPassword(t *testing.T) {
	// Given: a registered user and a request naming an unknown one
	env := newJWTEnv(t)
	env.addUser("alice", "s3cret")

	// When: the endpoint is asked for a token for an unknown user
	rec := env.request(http.MethodGet, "/token?service="+jwtTestService,
		[2]string{"mallory", "s3cret"})

	// Then: the rejection is byte-identical to the wrong-password one
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if rec.Body.String() != unauthorizedBody {
		t.Errorf("body = %q, want %q", rec.Body.String(), unauthorizedBody)
	}
}

// Test_TokenEndpoint_when_NoCredentials_then_401WithChallenge pins that a
// request with no pull scope still requires credentials. Anonymous tokens
// exist only for pull of a published repository.
func Test_TokenEndpoint_when_NoCredentials_then_401WithChallenge(t *testing.T) {
	// Given: the fixture
	env := newJWTEnv(t)

	// When: the endpoint is called without credentials
	rec := env.request(http.MethodGet, "/token?service="+jwtTestService, [2]string{})

	// Then: it answers 401 with the Basic challenge
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if got, want := rec.Header().Get("WWW-Authenticate"), `Basic realm="`+jwtTestService+`"`; got != want {
		t.Errorf("WWW-Authenticate = %q, want %q", got, want)
	}
}

// Test_TokenEndpoint_when_MissingService_then_400 pins that the service
// parameter is required (it names the audience).
func Test_TokenEndpoint_when_MissingService_then_400(t *testing.T) {
	// Given: a registered user and a request without the service parameter
	env := newJWTEnv(t)
	env.addUser("alice", "s3cret")

	// When: the endpoint is called
	rec := env.request(http.MethodGet, "/token", [2]string{"alice", "s3cret"})

	// Then: it answers 400
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if rec.Body.String() != missingService {
		t.Errorf("body = %q, want %q", rec.Body.String(), missingService)
	}
}

// Test_TokenEndpoint_when_UnknownService_then_400 pins that this server only
// issues tokens for its own configured service (audience).
func Test_TokenEndpoint_when_UnknownService_then_400(t *testing.T) {
	// Given: a registered user and a request naming another service
	env := newJWTEnv(t)
	env.addUser("alice", "s3cret")

	// When: the endpoint is called
	rec := env.request(http.MethodGet,
		"/token?service=registry.other.example", [2]string{"alice", "s3cret"})

	// Then: it answers 400
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if rec.Body.String() != unknownService {
		t.Errorf("body = %q, want %q", rec.Body.String(), unknownService)
	}
}

// Test_TokenEndpoint_when_MalformedScope_then_400 pins that a broken scope
// never reaches the token.
func Test_TokenEndpoint_when_MalformedScope_then_400(t *testing.T) {
	// Given: a registered user and a request whose scope has no actions
	env := newJWTEnv(t)
	env.addUser("alice", "s3cret")

	// When: the endpoint is called
	rec := env.request(http.MethodGet,
		"/token?service="+jwtTestService+"&scope=repository:name:", [2]string{"alice", "s3cret"})

	// Then: it answers 400 quoting the offending scope
	want := `{"error":"invalid scope \"repository:name:\": invalid actions \"\""}`
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if rec.Body.String() != want {
		t.Errorf("body = %q, want %q", rec.Body.String(), want)
	}
}

// Test_TokenEndpoint_when_MultipleScopes_then_AllEntriesInAccess pins the
// spec's repeatable scope parameter.
func Test_TokenEndpoint_when_MultipleScopes_then_AllEntriesInAccess(t *testing.T) {
	// Given: a registered user and a two-scope request
	env := newJWTEnv(t)
	env.addUser("alice", "s3cret")
	target := "/token?service=" + jwtTestService +
		"&scope=repository:acme/api:pull&scope=repository:acme/web:push"

	// When: the endpoint is called
	rec := env.request(http.MethodGet, target, [2]string{"alice", "s3cret"})

	// Then: the token carries both access entries
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	payload := decodeClaims(t, decodeTokenBody(t, rec).Token)
	access, _ := payload["access"].([]any)
	if len(access) != 2 {
		t.Fatalf("access = %v, want two entries", payload["access"])
	}
	first, _ := access[0].(map[string]any)
	second, _ := access[1].(map[string]any)
	if first["name"] != "acme/api" || second["name"] != "acme/web" {
		t.Errorf("entries = %v %v, want acme/api then acme/web", first, second)
	}
}

// Test_TokenEndpoint_when_NoScope_then_EmptyAccessArray pins the scope-less
// token (docker login only checks credentials).
func Test_TokenEndpoint_when_NoScope_then_EmptyAccessArray(t *testing.T) {
	// Given: a registered user and a request without scope
	env := newJWTEnv(t)
	env.addUser("alice", "s3cret")

	// When: the endpoint is called
	rec := env.request(http.MethodGet, "/token?service="+jwtTestService,
		[2]string{"alice", "s3cret"})

	// Then: the token is issued with an empty access array
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	payload := decodeClaims(t, decodeTokenBody(t, rec).Token)
	access, ok := payload["access"].([]any)
	if !ok || len(access) != 0 {
		t.Errorf("access = %v, want an empty array", payload["access"])
	}
}

// Test_TokenEndpoint_when_MethodNotAllowed_then_405 pins that only GET and
// POST are served: docker probes the token endpoint with POST first, so both
// verbs answer the same token contract and everything else is refused.
func Test_TokenEndpoint_when_MethodNotAllowed_then_405(t *testing.T) {
	// Given: the fixture
	env := newJWTEnv(t)

	// When: the endpoint is addressed with PUT
	rec := env.request(http.MethodPut, "/token?service="+jwtTestService, [2]string{})

	// Then: it is refused
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow != "GET, POST" {
		t.Errorf("Allow = %q, want %q", allow, "GET, POST")
	}
}

// Test_TokenEndpoint_when_Post_then_SameTokenAsGet pins the docker probe
// path: POST /token is answered with the same contract as GET (docker tries
// POST before GET when it discovers the token realm).
func Test_TokenEndpoint_when_Post_then_SameTokenAsGet(t *testing.T) {
	// Given: a registered user and a scoped token request
	env := newJWTEnv(t)
	env.addUser("alice", "s3cret")
	target := "/token?service=" + jwtTestService +
		"&scope=repository:library/nginx:pull,push"

	// When: the token endpoint is addressed with POST
	rec := env.request(http.MethodPost, target, [2]string{"alice", "s3cret"})

	// Then: it answers 200 with a token carrying the requested scope
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	payload := decodeClaims(t, decodeTokenBody(t, rec).Token)
	access, _ := payload["access"].([]any)
	if len(access) != 1 {
		t.Fatalf("access = %v, want one entry", payload["access"])
	}
	entry, _ := access[0].(map[string]any)
	if entry["name"] != "library/nginx" {
		t.Errorf("entry = %v, want repository library/nginx", entry)
	}
	if _, err := env.signer.Validate(decodeTokenBody(t, rec).Token); err != nil {
		t.Errorf("Validate(issued token): %v", err)
	}
}

// Test_TokenEndpoint_when_PostFormParams_then_TokenIssued pins that POST
// also accepts form-encoded parameters (the OAuth2 token endpoint shape)
// in addition to the query string.
func Test_TokenEndpoint_when_PostFormParams_then_TokenIssued(t *testing.T) {
	// Given: a registered user and a form-encoded POST body
	env := newJWTEnv(t)
	env.addUser("alice", "s3cret")
	form := url.Values{
		"service": {jwtTestService},
		"scope":   {"repository:acme/api:pull"},
	}
	req := httptest.NewRequest(http.MethodPost, "/token",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("alice", "s3cret")
	rec := httptest.NewRecorder()
	env.handler.ServeHTTP(rec, req)

	// Then: it answers 200 with a token carrying the posted scope
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	payload := decodeClaims(t, decodeTokenBody(t, rec).Token)
	access, _ := payload["access"].([]any)
	if len(access) != 1 {
		t.Fatalf("access = %v, want one entry", payload["access"])
	}
	entry, _ := access[0].(map[string]any)
	if entry["name"] != "acme/api" {
		t.Errorf("entry = %v, want repository acme/api", entry)
	}
}

// Test_TokenEndpoint_when_EmptyScopeParam_then_Skipped pins the docker
// scope shape "scope=&scope=repository:...": an empty scope parameter is not
// a malformed scope — it is ignored, and the real scope is still issued.
func Test_TokenEndpoint_when_EmptyScopeParam_then_Skipped(t *testing.T) {
	// Given: a registered user and a request with an empty scope first
	env := newJWTEnv(t)
	env.addUser("alice", "s3cret")
	target := "/token?service=" + jwtTestService +
		"&scope=&scope=repository:acme/api:pull"

	// When: the token endpoint is called
	rec := env.request(http.MethodGet, target, [2]string{"alice", "s3cret"})

	// Then: it answers 200 and only the real scope reaches the access claim
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	payload := decodeClaims(t, decodeTokenBody(t, rec).Token)
	access, _ := payload["access"].([]any)
	if len(access) != 1 {
		t.Fatalf("access = %v, want one entry", payload["access"])
	}
	entry, _ := access[0].(map[string]any)
	if entry["name"] != "acme/api" {
		t.Errorf("entry = %v, want repository acme/api", entry)
	}
}

// Test_TokenEndpoint_when_OnlyEmptyScopes_then_EmptyAccessArray pins that a
// request made entirely of empty scope parameters behaves like the
// scope-less request (docker login's credential check).
func Test_TokenEndpoint_when_OnlyEmptyScopes_then_EmptyAccessArray(t *testing.T) {
	// Given: a registered user and a request with only empty scope params
	env := newJWTEnv(t)
	env.addUser("alice", "s3cret")
	target := "/token?service=" + jwtTestService + "&scope=&scope="

	// When: the token endpoint is called
	rec := env.request(http.MethodGet, target, [2]string{"alice", "s3cret"})

	// Then: the token is issued with an empty access array
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	payload := decodeClaims(t, decodeTokenBody(t, rec).Token)
	access, ok := payload["access"].([]any)
	if !ok || len(access) != 0 {
		t.Errorf("access = %v, want an empty array", payload["access"])
	}
}
