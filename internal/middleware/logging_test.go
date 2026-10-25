package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BRAVO68WEB/wharf/internal/middleware"
)

// Test_RequestLogger_when_InboundRequestID_then_Reused pins that a
// well-formed X-Request-ID from a proxy is echoed, not replaced.
func Test_RequestLogger_when_InboundRequestID_then_Reused(t *testing.T) {
	// Given: a request carrying a proxy-supplied request ID.
	lc := newLogCapture()
	h := middleware.RequestLogger(lc.logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.Header.Set(middleware.RequestIDHeader, "proxy-abc-123")
	rr := httptest.NewRecorder()

	// When: the request is served.
	h.ServeHTTP(rr, req)

	// Then: the response and the log both carry the inbound ID.
	if got := rr.Header().Get(middleware.RequestIDHeader); got != "proxy-abc-123" {
		t.Fatalf("response %s = %q, want %q", middleware.RequestIDHeader, got, "proxy-abc-123")
	}
	if m := lc.line(t, 0); m["request_id"] != "proxy-abc-123" {
		t.Fatalf("request_id = %v, want proxy-abc-123", m["request_id"])
	}
}

// Test_RequestLogger_when_NoRequestID_then_Generated pins the generated-ID
// path: present on the response, non-empty in the log.
func Test_RequestLogger_when_NoRequestID_then_Generated(t *testing.T) {
	// Given: a request with no inbound X-Request-ID.
	lc := newLogCapture()
	h := middleware.RequestLogger(lc.logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rr := httptest.NewRecorder()

	// When: the request is served.
	h.ServeHTTP(rr, req)

	// Then: a generated ID is set on the response and logged.
	got := rr.Header().Get(middleware.RequestIDHeader)
	if got == "" {
		t.Fatal("generated request ID is empty")
	}
	if len(got) != 32 {
		t.Fatalf("generated request ID = %q, want 32 hex chars", got)
	}
	if m := lc.line(t, 0); m["request_id"] != got {
		t.Fatalf("logged request_id = %v, want %q", m["request_id"], got)
	}
}

// Test_RequestLogger_when_HostileRequestID_then_Replaced pins that a header
// with control characters is discarded, never echoed into logs or responses.
func Test_RequestLogger_when_HostileRequestID_then_Replaced(t *testing.T) {
	// Given: a request whose X-Request-ID carries an injection attempt.
	lc := newLogCapture()
	h := middleware.RequestLogger(lc.logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.Header.Set(middleware.RequestIDHeader, "bad\tid\ninjected")
	rr := httptest.NewRecorder()

	// When: the request is served.
	h.ServeHTTP(rr, req)

	// Then: a fresh ID replaces it everywhere.
	got := rr.Header().Get(middleware.RequestIDHeader)
	if strings.Contains(got, "injected") {
		t.Fatalf("hostile request ID echoed: %q", got)
	}
	if out := lc.buf.String(); strings.Contains(out, "injected") {
		t.Fatalf("hostile request ID leaked into log: %s", out)
	}
}

// Test_RequestLogger_when_HealthCheck_then_NotLogged pins the noise
// reduction: probe routes produce no log line and no request-ID header work.
func Test_RequestLogger_when_HealthCheck_then_NotLogged(t *testing.T) {
	// Given: a request to a health probe path.
	lc := newLogCapture()
	h := middleware.RequestLogger(lc.logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()

	// When: the request is served.
	h.ServeHTTP(rr, req)

	// Then: the handler runs but nothing is logged.
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if out := strings.TrimSpace(lc.buf.String()); out != "" {
		t.Fatalf("health check logged: %s", out)
	}
}

// Test_RequestLogger_when_HandlerThen_StatusAndBytesRecorded pins the
// status/bytes capture against a non-default status and body size.
func Test_RequestLogger_when_HandlerThen_StatusAndBytesRecorded(t *testing.T) {
	// Given: a handler answering 404 with a 10-byte body.
	lc := newLogCapture()
	h := middleware.RequestLogger(lc.logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("0123456789"))
	}))
	req := httptest.NewRequest(http.MethodGet, "/dashboard/missing", nil)
	rr := httptest.NewRecorder()

	// When: the request is served.
	h.ServeHTTP(rr, req)

	// Then: the log records the real status and byte count.
	m := lc.line(t, 0)
	if m["status"] != float64(http.StatusNotFound) {
		t.Fatalf("status = %v, want 404", m["status"])
	}
	if m["bytes"] != float64(10) {
		t.Fatalf("bytes = %v, want 10", m["bytes"])
	}
	if m["method"] != "GET" || m["path"] != "/dashboard/missing" {
		t.Fatalf("method/path = %v/%v", m["method"], m["path"])
	}
	if _, ok := m["duration"]; !ok {
		t.Fatal("duration field missing from log line")
	}
}

// Test_RequestLogger_when_NoAuthorization_then_NoRedactedField pins that the
// authorization field only appears when a credential was sent (clean logs).
func Test_RequestLogger_when_NoAuthorization_then_NoRedactedField(t *testing.T) {
	// Given: a request without credentials.
	lc := newLogCapture()
	h := middleware.RequestLogger(lc.logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rr := httptest.NewRecorder()

	// When: the request is served.
	h.ServeHTTP(rr, req)

	// Then: no authorization field is logged.
	if _, ok := lc.line(t, 0)["authorization"]; ok {
		t.Fatal("authorization field logged for a credential-less request")
	}
}

// Test_RequestLogger_when_NilLogger_then_FallsBackToDefault pins the nil
// logger contract: no panic, request still served.
func Test_RequestLogger_when_NilLogger_then_FallsBackToDefault(t *testing.T) {
	// Given: RequestLogger built with a nil logger.
	called := false
	h := middleware.RequestLogger(nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rr := httptest.NewRecorder()

	// When: the request is served.
	h.ServeHTTP(rr, req)

	// Then: the handler runs normally.
	if !called || rr.Code != http.StatusOK {
		t.Fatalf("called = %v, status = %d; want true, 200", called, rr.Code)
	}
}
