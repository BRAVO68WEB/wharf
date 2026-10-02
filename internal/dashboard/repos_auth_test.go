package dashboard_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/user/better-docker-registry/internal/auth"
	"github.com/user/better-docker-registry/internal/config"
	"github.com/user/better-docker-registry/internal/dashboard"
	"github.com/user/better-docker-registry/internal/db"
	"github.com/user/better-docker-registry/internal/middleware"
)

// repoTestAuthRouter wires login + the CSRF-guarded dashboard tree the way the
// integrating task will: RequireSession injects the session, WithSessionCSRF
// copies its token into middleware.CSRF's context slot, then CSRF enforces it.
func repoTestAuthRouter(t *testing.T) (http.Handler, db.Querier) {
	t.Helper()
	q := repoTestDB(t)
	manager, err := auth.NewSessionManager(q, config.SessionConfig{
		Secret: config.Secret("repo-test-session-secret"),
		TTL:    config.Duration(time.Hour),
	}, false)
	if err != nil {
		t.Fatalf("NewSessionManager: %v", err)
	}

	r := chi.NewRouter()
	r.Post(auth.SessionLoginPath, manager.HandleLogin)
	r.Group(func(r chi.Router) {
		r.Use(manager.RequireSession)
		r.Use(dashboard.WithSessionCSRF)
		r.Use(middleware.CSRF)
		dashboard.NewRepoHandler(q).Mount(r)
		r.Post(auth.SessionLogoutPath, manager.HandleLogout)
	})
	return r, q
}

// repoTestLogin posts credentials and returns the session cookies it set.
func repoTestLogin(t *testing.T, h http.Handler) []*http.Cookie {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, auth.SessionLoginPath,
		strings.NewReader("username=operator&password=pw"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("login set no session cookie")
	}
	return cookies
}

// repoTestPage fetches the browse page under the given session and returns the
// CSRF token it renders into hx-headers.
func repoTestPage(t *testing.T, h http.Handler, cookies []*http.Cookie) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("page status = %d, want %d", rec.Code, http.StatusOK)
	}
	token := repoTestCSRFToken(t, rec.Body.String())
	if token == "" {
		t.Fatal("page rendered no CSRF token into hx-headers")
	}
	return token
}

// repoTestPost issues a POST carrying the given cookies and headers.
func repoTestPost(t *testing.T, h http.Handler, path string, cookies []*http.Cookie, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Given: a signed-in operator.
// When:  GET /dashboard through RequireSession + WithSessionCSRF, then a
//
//	state-changing request carrying the rendered token.
//
// Then:  the token satisfies middleware.CSRF, HTMX callers get an HX-Redirect
//
//	instead of a swap, and a bare request is still refused.
func Test_dashboardAuth_when_session_then_csrfTokenRenderedAndAccepted(t *testing.T) {
	r, q := repoTestAuthRouter(t)
	repoTestSeed(t, q, "alpha", 1, 1)

	hash, err := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword: %v", err)
	}
	if _, err := q.CreateUser(t.Context(), db.CreateUserParams{
		Username:     "operator",
		PasswordHash: string(hash),
	}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	cookies := repoTestLogin(t, r)
	token := repoTestPage(t, r, cookies)

	// Then: a state-changing request without the token is refused.
	bare := repoTestPost(t, r, auth.SessionLogoutPath, cookies, nil)
	if bare.Code != http.StatusForbidden {
		t.Errorf("logout without CSRF token: status = %d, want %d", bare.Code, http.StatusForbidden)
	}

	// And: the rendered token satisfies middleware.CSRF on an HTMX logout,
	// which answers with a full-page redirect rather than a swap.
	htmx := repoTestPost(t, r, auth.SessionLogoutPath, cookies, map[string]string{
		middleware.CSRFHeader: token,
		"HX-Request":          "true",
	})
	if htmx.Code == http.StatusForbidden {
		t.Fatalf("HTMX logout rejected the rendered CSRF token: %d", htmx.Code)
	}
	if got := htmx.Header().Get("HX-Redirect"); got != auth.SessionLoginPath {
		t.Errorf("HX-Redirect = %q, want %q", got, auth.SessionLoginPath)
	}

	// And: a non-HTMX logout with the token is accepted too.
	cookies = repoTestLogin(t, r)
	token = repoTestPage(t, r, cookies)
	plain := repoTestPost(t, r, auth.SessionLogoutPath, cookies, map[string]string{middleware.CSRFHeader: token})
	if plain.Code != http.StatusSeeOther {
		t.Errorf("logout status = %d, want %d", plain.Code, http.StatusSeeOther)
	}
}
