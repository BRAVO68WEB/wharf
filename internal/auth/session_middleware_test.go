package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/user/better-docker-registry/internal/auth"
)

func Test_MiddlewareRedirects_when_NoCookie(t *testing.T) {
	// Given: a protected route and no session cookie.
	m, _ := newSessionTestManager(t, time.Hour, true)
	h := newSessionTestMux(m)

	// When: the protected route is requested bare.
	resp := sessionGet(t, h, "/protected")

	// Then: the client is redirected to the login page and the handler does
	// not run. The redirect names the request's own host:port.
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusFound)
	}
	want := "http://example.com" + auth.SessionLoginPath
	if loc := resp.Header.Get("Location"); loc != want {
		t.Errorf("Location = %q, want %q", loc, want)
	}
	if resp.Header.Get("X-Test-User") != "" {
		t.Error("protected handler ran without a session")
	}
}

func Test_MiddlewareRedirects_when_CookieSignatureTampered(t *testing.T) {
	// Given: a valid cookie for a live session, tampered with afterwards.
	m, q := newSessionTestManager(t, time.Hour, true)
	newSessionTestUser(t, q, "alice", "s3cret", false)
	h := newSessionTestMux(m)
	cookie := sessionLogin(t, h, "alice", "s3cret")
	flipped := "A"
	if cookie.Value[len(cookie.Value)-1] == 'A' {
		flipped = "B"
	}
	tampered := *cookie
	tampered.Value = cookie.Value[:len(cookie.Value)-1] + flipped

	// When: the protected route is requested with the tampered cookie.
	resp := sessionGet(t, h, "/protected", &tampered)

	// Then: the signature check rejects it even though the session row is
	// still live, and the untouched cookie still passes.
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("tampered status = %d, want %d", resp.StatusCode, http.StatusFound)
	}
	if got := sessionGet(t, h, "/protected", cookie); got.StatusCode != http.StatusOK {
		t.Errorf("original cookie status = %d, want %d", got.StatusCode, http.StatusOK)
	}
}

func Test_MiddlewareRedirects_when_SessionRowDeleted(t *testing.T) {
	// Given: a valid cookie whose session row was removed server-side.
	m, q := newSessionTestManager(t, time.Hour, true)
	newSessionTestUser(t, q, "alice", "s3cret", false)
	h := newSessionTestMux(m)
	cookie := sessionLogin(t, h, "alice", "s3cret")
	if err := q.DeleteSession(t.Context(), sessionRawToken(t, cookie)); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	// When: the protected route is requested with that cookie.
	resp := sessionGet(t, h, "/protected", cookie)

	// Then: the request is rejected to the login page.
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusFound)
	}
	want := "http://example.com" + auth.SessionLoginPath
	if loc := resp.Header.Get("Location"); loc != want {
		t.Errorf("Location = %q, want %q", loc, want)
	}
}

func Test_MiddlewareRedirects_when_HXRequestHeader_then_stillRedirects(t *testing.T) {
	// Given: a request that still carries the old HTMX header and no session.
	m, _ := newSessionTestManager(t, time.Hour, true)
	h := newSessionTestMux(m)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()

	// When: the protected route is requested.
	h.ServeHTTP(rec, req)
	resp := rec.Result()

	// Then: the header is ignored. The client gets the same login redirect
	// as any other browser request.
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusFound)
	}
	if got := resp.Header.Get("HX-Redirect"); got != "" {
		t.Errorf("HX-Redirect = %q, want empty", got)
	}
	want := "http://example.com" + auth.SessionLoginPath
	if loc := resp.Header.Get("Location"); loc != want {
		t.Errorf("Location = %q, want %q", loc, want)
	}
}

func Test_LogoutSucceeds_when_NoCookiePresent(t *testing.T) {
	// Given: a logged-out client with no cookie at all.
	m, _ := newSessionTestManager(t, time.Hour, true)
	h := newSessionTestMux(m)

	// When: logout is posted bare.
	resp := sessionPostForm(t, h, auth.SessionLogoutPath, nil)

	// Then: logout still redirects to the login page and clears the cookie.
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusSeeOther)
	}
	if c := sessionCookie(t, resp); c.MaxAge >= 0 {
		t.Errorf("cleared cookie MaxAge = %d, want < 0", c.MaxAge)
	}
}

func Test_LogoutClearsCookie_when_CookieTampered(t *testing.T) {
	// Given: a tampered cookie for an otherwise live session.
	m, q := newSessionTestManager(t, time.Hour, true)
	newSessionTestUser(t, q, "alice", "s3cret", false)
	h := newSessionTestMux(m)
	cookie := sessionLogin(t, h, "alice", "s3cret")
	token := sessionRawToken(t, cookie)
	tampered := *cookie
	tampered.Value = cookie.Value + "x"

	// When: logout is posted with the tampered cookie.
	resp := sessionPostForm(t, h, auth.SessionLogoutPath, nil, &tampered)

	// Then: the client cookie is cleared and redirected; the unreadable
	// token cannot be deleted, so that row simply dies with its TTL.
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusSeeOther)
	}
	if c := sessionCookie(t, resp); c.MaxAge >= 0 {
		t.Errorf("cleared cookie MaxAge = %d, want < 0", c.MaxAge)
	}
	if _, err := q.GetSession(t.Context(), token); err != nil {
		t.Errorf("GetSession after tampered logout = %v, want row kept (token unreadable)", err)
	}
}
