package middleware_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/user/better-docker-registry/internal/middleware"
)

// logCapture is a slog logger writing JSON lines into an inspectable buffer.
type logCapture struct {
	logger *slog.Logger
	buf    *bytes.Buffer
}

func newLogCapture() *logCapture {
	var buf bytes.Buffer
	return &logCapture{logger: slog.New(slog.NewJSONHandler(&buf, nil)), buf: &buf}
}

// line decodes the nth captured log line as a JSON object.
func (c *logCapture) line(t *testing.T, n int) map[string]any {
	t.Helper()
	raw := strings.TrimSpace(c.buf.String())
	if raw == "" {
		t.Fatal("expected a log line, got none")
	}
	lines := strings.Split(raw, "\n")
	if n >= len(lines) {
		t.Fatalf("expected at least %d log lines, got %d", n+1, len(lines))
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[n]), &m); err != nil {
		t.Fatalf("log line %d is not JSON: %v\n%s", n, err, lines[n])
	}
	return m
}

// Test_CSRF_when_PostWithoutToken_then_403 pins the plan's failure QA
// scenario: a dashboard mutation without the CSRF header must be refused.
func Test_CSRF_when_PostWithoutToken_then_403(t *testing.T) {
	// Given: a POST with a session-bound CSRF token in context but no
	// X-CSRF-Token header.
	called := false
	h := middleware.CSRF(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/dashboard/repos/test/delete", nil)
	req = req.WithContext(middleware.WithCSRFToken(req.Context(), "tok-123"))
	rr := httptest.NewRecorder()

	// When: the request is served.
	h.ServeHTTP(rr, req)

	// Then: it is refused with 403 and the handler never runs.
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
	if called {
		t.Fatal("handler ran on a CSRF-forbidden request")
	}
}

// Test_CSRF_when_PostWithMatchingToken_then_Passes pins the plan's happy QA
// scenario: a dashboard mutation with the matching token reaches the handler.
func Test_CSRF_when_PostWithMatchingToken_then_Passes(t *testing.T) {
	// Given: a POST whose X-CSRF-Token header matches the session token.
	called := false
	h := middleware.CSRF(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/dashboard/repos/test/delete", nil)
	req = req.WithContext(middleware.WithCSRFToken(req.Context(), "tok-123"))
	req.Header.Set(middleware.CSRFHeader, "tok-123")
	rr := httptest.NewRecorder()

	// When: the request is served.
	h.ServeHTTP(rr, req)

	// Then: it reaches the handler with 200.
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !called {
		t.Fatal("handler did not run on a valid CSRF request")
	}
}

// Test_CSRF_when_V2MutationWithoutToken_then_Passes pins the plan's MUST NOT:
// Docker clients never send CSRF tokens, so /v2/* is exempt.
func Test_CSRF_when_V2MutationWithoutToken_then_Passes(t *testing.T) {
	// Given: a token-less PUT to the Docker registry API tree.
	called := false
	h := middleware.CSRF(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusCreated)
	}))
	req := httptest.NewRequest(http.MethodPut, "/v2/library/nginx/manifests/latest", nil)
	rr := httptest.NewRecorder()

	// When: the request is served.
	h.ServeHTTP(rr, req)

	// Then: the middleware is not applied and the handler runs.
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rr.Code)
	}
	if !called {
		t.Fatal("handler did not run on a /v2/* request")
	}
}

// Test_SecurityHeaders_when_DashboardRoute_then_AllHeadersPresent pins the
// hardening headers on dashboard (non-/v2) responses.
func Test_SecurityHeaders_when_DashboardRoute_then_AllHeadersPresent(t *testing.T) {
	// Given: the security-headers middleware around a dashboard handler.
	h := middleware.SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/dashboard/repos", nil)
	rr := httptest.NewRecorder()

	// When: the request is served.
	h.ServeHTTP(rr, req)

	// Then: nosniff, DENY framing and a strict CSP are all set.
	if got := rr.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want %q", got, "nosniff")
	}
	if got := rr.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options = %q, want %q", got, "DENY")
	}
	csp := rr.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("CSP missing frame-ancestors 'none': %q", csp)
	}
	if !strings.Contains(csp, "default-src 'self'") {
		t.Fatalf("CSP missing default-src 'self': %q", csp)
	}
	if !strings.Contains(csp, "script-src 'self'") {
		t.Fatalf("CSP missing script-src 'self': %q", csp)
	}
}

// Test_RequestLogger_when_AuthorizationPresent_then_ValueRedacted pins the
// plan's redaction requirement: Authorization values must never reach the log.
func Test_RequestLogger_when_AuthorizationPresent_then_ValueRedacted(t *testing.T) {
	// Given: a request carrying a Bearer credential.
	lc := newLogCapture()
	h := middleware.RequestLogger(lc.logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/dashboard/repos/test/delete", nil)
	req.Header.Set("Authorization", "Bearer eyJhbGciOiJIUzI1NiJ9.payload.sig")
	rr := httptest.NewRecorder()

	// When: the request is served.
	h.ServeHTTP(rr, req)

	// Then: the captured log has no trace of the credential and marks the
	// header as redacted.
	out := lc.buf.String()
	t.Logf("captured log: %s", strings.TrimSpace(out))
	if strings.Contains(out, "Bearer eyJ") {
		t.Fatal("log leaked the Authorization credential (grep 'Bearer eyJ' matched)")
	}
	if strings.Contains(out, "eyJhbGciOiJIUzI1NiJ9") {
		t.Fatal("log leaked part of the JWT")
	}
	m := lc.line(t, 0)
	if got := m["authorization"]; got != "REDACTED" {
		t.Fatalf("authorization field = %v, want REDACTED", got)
	}
}

// Test_RequestLogger_when_GeneralRequest_then_StructuredJSON pins the log
// shape: one JSON object per request with the required fields.
func Test_RequestLogger_when_GeneralRequest_then_StructuredJSON(t *testing.T) {
	// Given: a logging middleware around a handler writing a 418 body.
	lc := newLogCapture()
	h := middleware.RequestLogger(lc.logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("hello"))
	}))
	req := httptest.NewRequest(http.MethodGet, "/dashboard/repos", nil)
	rr := httptest.NewRecorder()

	// When: the request is served.
	h.ServeHTTP(rr, req)

	// Then: one JSON line carries method, path, status, duration, bytes and
	// request_id.
	m := lc.line(t, 0)
	for _, key := range []string{"method", "path", "status", "duration", "bytes", "request_id"} {
		if _, ok := m[key]; !ok {
			t.Fatalf("log line missing %q field: %v", key, m)
		}
	}
	if m["method"] != "GET" {
		t.Fatalf("method = %v, want GET", m["method"])
	}
	if m["path"] != "/dashboard/repos" {
		t.Fatalf("path = %v, want /dashboard/repos", m["path"])
	}
	if m["status"] != float64(http.StatusTeapot) {
		t.Fatalf("status = %v, want 418", m["status"])
	}
	if m["bytes"] != float64(5) {
		t.Fatalf("bytes = %v, want 5", m["bytes"])
	}
}
