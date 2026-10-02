package auth_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/user/better-docker-registry/internal/auth"
)

// The login page contract (dashboard final wave): GET /dashboard/login
// renders the sign-in form, and every redirect to it names the request's own
// host and port — a redirect may never carry a hard-coded port.

func Test_LoginPageRendersForm_when_RequestedViaGET(t *testing.T) {
	// Given: the auth routes mounted and a client with no session.
	m, _ := newSessionTestManager(t, time.Hour, true)
	h := newSessionTestMux(m)

	// When: the login page is requested with GET.
	resp := sessionGet(t, h, auth.SessionLoginPath)

	// Then: an HTML login form posting to the login endpoint is rendered.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	for _, want := range []string{
		`method="post"`,
		`name="username"`,
		`name="password"`,
		auth.SessionLoginPath,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("login page body missing %q", want)
		}
	}
}

func Test_DenyRedirectUsesRequestHost_when_NoCookie(t *testing.T) {
	// Given: a protected route and a request addressed to localhost:5000.
	m, _ := newSessionTestManager(t, time.Hour, true)
	h := newSessionTestMux(m)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Host = "localhost:5000"
	rec := httptest.NewRecorder()

	// When: the protected route is requested without a session.
	h.ServeHTTP(rec, req)
	resp := rec.Result()

	// Then: the redirect targets the login page on the request's own
	// host:port — never a hard-coded one.
	want := "http://localhost:5000" + auth.SessionLoginPath
	if loc := resp.Header.Get("Location"); loc != want {
		t.Errorf("Location = %q, want %q", loc, want)
	}
}
