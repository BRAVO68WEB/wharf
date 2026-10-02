// Core acceptance tests for dashboard session auth: login sets a cookie, a
// valid cookie passes RequireSession, an expired session is rejected, and
// logout clears the session. This file is self-contained so
// `go test ./internal/auth/session_test.go` runs it standalone.
package auth_test

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/user/better-docker-registry/internal/auth"
	"github.com/user/better-docker-registry/internal/config"
	"github.com/user/better-docker-registry/internal/db"
)

const sessionTestSecret = "test-session-signing-secret"

// newSessionTestDB opens a fresh migrated SQLite database per test.
func newSessionTestDB(t *testing.T) db.Querier {
	t.Helper()
	sqlDB, err := db.OpenAndMigrate(t.Context(), filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db.New(sqlDB)
}

// newSessionTestManager wires a manager over a fresh database.
func newSessionTestManager(t *testing.T, ttl time.Duration, cookieSecure bool) (*auth.SessionManager, db.Querier) {
	t.Helper()
	q := newSessionTestDB(t)
	m, err := auth.NewSessionManager(q, sessionTestConfig(ttl), cookieSecure)
	if err != nil {
		t.Fatalf("NewSessionManager: %v", err)
	}
	return m, q
}

// sessionTestConfig is the manager configuration with a fixed test secret.
func sessionTestConfig(ttl time.Duration) config.SessionConfig {
	return config.SessionConfig{Secret: config.Secret(sessionTestSecret), TTL: config.Duration(ttl)}
}

// newSessionTestUser inserts a user with a cheap bcrypt hash (MinCost keeps
// the suite fast; production hashes come from DefaultCost).
func newSessionTestUser(t *testing.T, q db.Querier, username, password string, isAdmin bool) db.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	u, err := q.CreateUser(t.Context(), db.CreateUserParams{
		Username:     username,
		PasswordHash: string(hash),
		IsAdmin:      isAdmin,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return u
}

// newSessionTestMux mounts the auth routes plus a protected handler that
// echoes the authenticated username.
func newSessionTestMux(m *auth.SessionManager) http.Handler {
	r := chi.NewRouter()
	m.Mount(r)
	protected := m.RequireSession(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		u, ok := auth.SessionUserFromContext(req.Context())
		if !ok {
			http.Error(w, "no session user in context", http.StatusInternalServerError)
			return
		}
		w.Header().Set("X-Test-User", u.Username)
		w.WriteHeader(http.StatusOK)
	}))
	r.Get("/protected", protected.ServeHTTP)
	return r
}

// sessionPostForm posts a form through the mux.
func sessionPostForm(t *testing.T, h http.Handler, path string, form url.Values, cookies ...*http.Cookie) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

// sessionGet issues a GET through the mux with optional cookies.
func sessionGet(t *testing.T, h http.Handler, path string, cookies ...*http.Cookie) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

// sessionCookie returns the session cookie set by a response.
func sessionCookie(t *testing.T, resp *http.Response) *http.Cookie {
	t.Helper()
	for _, c := range resp.Cookies() {
		if c.Name == auth.SessionCookieName {
			return c
		}
	}
	t.Fatalf("no %q cookie in response", auth.SessionCookieName)
	return nil
}

// sessionRawToken splits the signed cookie value back into the raw token.
func sessionRawToken(t *testing.T, c *http.Cookie) string {
	t.Helper()
	token, sig, ok := strings.Cut(c.Value, ".")
	if !ok || token == "" || sig == "" {
		t.Fatalf("cookie value %q is not <token>.<sig>", c.Value)
	}
	return token
}

// sessionLogin logs in and returns the session cookie.
func sessionLogin(t *testing.T, h http.Handler, username, password string) *http.Cookie {
	t.Helper()
	resp := sessionPostForm(t, h, auth.SessionLoginPath, url.Values{"username": {username}, "password": {password}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login status = %d, want %d", resp.StatusCode, http.StatusSeeOther)
	}
	return sessionCookie(t, resp)
}

func Test_LoginSetsSignedHttpOnlyCookie_when_CredentialsValid(t *testing.T) {
	// Given: a user with a known password and a manager with a 1h TTL.
	m, q := newSessionTestManager(t, time.Hour, true)
	user := newSessionTestUser(t, q, "alice", "s3cret", false)
	h := newSessionTestMux(m)

	// When: the login form is posted with the correct credentials.
	resp := sessionPostForm(t, h, auth.SessionLoginPath, url.Values{"username": {"alice"}, "password": {"s3cret"}})

	// Then: a hardened session cookie is set and the session row holds the
	// raw random token with the configured TTL.
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusSeeOther)
	}
	if loc := resp.Header.Get("Location"); loc != "/dashboard" {
		t.Errorf("Location = %q, want %q", loc, "/dashboard")
	}
	c := sessionCookie(t, resp)
	if !c.HttpOnly {
		t.Error("cookie is not HttpOnly")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Errorf("SameSite = %v, want Strict", c.SameSite)
	}
	if !c.Secure {
		t.Error("cookie is not Secure")
	}
	if c.Path != "/" {
		t.Errorf("Path = %q, want %q", c.Path, "/")
	}
	token := sessionRawToken(t, c)
	row, err := q.GetSession(t.Context(), token)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if row.UserID != user.ID {
		t.Errorf("session user_id = %d, want %d", row.UserID, user.ID)
	}
	want := time.Now().Add(time.Hour).Unix()
	if row.ExpiresAt < want-5 || row.ExpiresAt > want+5 {
		t.Errorf("expires_at = %d, want ~%d", row.ExpiresAt, want)
	}
}

func Test_SessionPassesMiddleware_when_CookieValid(t *testing.T) {
	// Given: a logged-in admin user with a valid session cookie.
	m, q := newSessionTestManager(t, time.Hour, true)
	user := newSessionTestUser(t, q, "alice", "s3cret", true)
	h := newSessionTestMux(m)
	cookie := sessionLogin(t, h, "alice", "s3cret")
	token := sessionRawToken(t, cookie)

	// When: a protected route is requested with that cookie.
	resp := sessionGet(t, h, "/protected", cookie)

	// Then: the request passes and the session user is in the context.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if got := resp.Header.Get("X-Test-User"); got != "alice" {
		t.Errorf("X-Test-User = %q, want %q", got, "alice")
	}
	// Context accessors are asserted through a second wrapped request.
	wrapped := m.RequireSession(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, ok := auth.SessionFromContext(r.Context())
		if !ok {
			t.Error("SessionFromContext found no session")
			return
		}
		if sess.Token != token {
			t.Errorf("session token = %q, want %q", sess.Token, token)
		}
		if sess.User.ID != user.ID || sess.User.Username != "alice" || !sess.User.IsAdmin {
			t.Errorf("session user = %+v, want id=%d alice admin", sess.User, user.ID)
		}
		if ctxToken, ok := auth.SessionTokenFromContext(r.Context()); !ok || ctxToken != token {
			t.Errorf("SessionTokenFromContext = %q,%v want %q,true", ctxToken, ok, token)
		}
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req2.AddCookie(cookie)
	wrapped.ServeHTTP(rec, req2)
	if rec.Code != http.StatusOK {
		t.Fatalf("wrapped status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func Test_MiddlewareRedirects_when_SessionExpired(t *testing.T) {
	// Given: a valid cookie whose session row has been aged past its TTL.
	m, q := newSessionTestManager(t, time.Hour, true)
	user := newSessionTestUser(t, q, "alice", "s3cret", false)
	h := newSessionTestMux(m)
	cookie := sessionLogin(t, h, "alice", "s3cret")
	token := sessionRawToken(t, cookie)
	if err := q.DeleteSession(t.Context(), token); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	err := q.CreateSession(t.Context(), db.CreateSessionParams{
		Token:     token,
		UserID:    user.ID,
		ExpiresAt: time.Now().Add(-time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// When: the protected route is requested with the expired cookie.
	resp := sessionGet(t, h, "/protected", cookie)

	// Then: the client is redirected to the login page and the stale row is
	// cleaned up.
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusFound)
	}
	if loc := resp.Header.Get("Location"); loc != "http://example.com"+auth.SessionLoginPath {
		t.Errorf("Location = %q, want %q", loc, "http://example.com"+auth.SessionLoginPath)
	}
	if resp.Header.Get("X-Test-User") != "" {
		t.Error("protected handler ran for an expired session")
	}
	if _, err := q.GetSession(t.Context(), token); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetSession after expiry = %v, want sql.ErrNoRows", err)
	}
}

func Test_LogoutClearsCookieAndSession_when_CookieValid(t *testing.T) {
	// Given: a logged-in user with a live session.
	m, q := newSessionTestManager(t, time.Hour, true)
	newSessionTestUser(t, q, "alice", "s3cret", false)
	h := newSessionTestMux(m)
	cookie := sessionLogin(t, h, "alice", "s3cret")
	token := sessionRawToken(t, cookie)

	// When: logout is posted with the session cookie.
	resp := sessionPostForm(t, h, auth.SessionLogoutPath, nil, cookie)

	// Then: the cookie is cleared, the row is deleted, and the old cookie is
	// no longer accepted.
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusSeeOther)
	}
	if loc := resp.Header.Get("Location"); loc != "http://example.com"+auth.SessionLoginPath {
		t.Errorf("Location = %q, want %q", loc, "http://example.com"+auth.SessionLoginPath)
	}
	cleared := sessionCookie(t, resp)
	if cleared.MaxAge >= 0 {
		t.Errorf("cleared cookie MaxAge = %d, want < 0", cleared.MaxAge)
	}
	if _, err := q.GetSession(t.Context(), token); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetSession after logout = %v, want sql.ErrNoRows", err)
	}
	after := sessionGet(t, h, "/protected", cookie)
	if after.StatusCode != http.StatusFound {
		t.Errorf("status with logged-out cookie = %d, want %d", after.StatusCode, http.StatusFound)
	}
}
