package auth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BRAVO68WEB/wharf/internal/auth"
	"github.com/BRAVO68WEB/wharf/internal/dashboard"
	"github.com/BRAVO68WEB/wharf/internal/middleware"
)

func TestJSONLogin_returnsCSRFAndCookie(t *testing.T) {
	m, q := newSessionTestManager(t, time.Hour, false)
	newSessionTestUser(t, q, "admin", "admin", true)
	h := jsonSessionMux(m)

	rec := jsonPost(t, h, "/dashboard/api/login", `{"username":"admin","password":"admin"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("login = %d body=%s", rec.Code, rec.Body.String())
	}
	var view struct {
		Username  string `json:"username"`
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Username != "admin" || view.CSRFToken == "" {
		t.Fatalf("view = %#v", view)
	}
	cookie := rec.Result().Cookies()
	if len(cookie) == 0 || cookie[0].Name != auth.SessionCookieName {
		t.Fatalf("cookies = %#v", cookie)
	}
	if token := sessionRawToken(t, cookie[0]); token != view.CSRFToken {
		t.Fatalf("csrf = %q, cookie token = %q", view.CSRFToken, token)
	}

	bad := jsonPost(t, h, "/dashboard/api/login", `{"username":"admin","password":"nope"}`, nil)
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("bad login = %d", bad.Code)
	}

	session := jsonDo(t, h, http.MethodGet, "/dashboard/api/session", "", cookie[0], "")
	if session.Code != http.StatusOK {
		t.Fatalf("session = %d body=%s", session.Code, session.Body.String())
	}

	noToken := jsonDo(t, h, http.MethodPost, "/dashboard/api/logout", "", cookie[0], "")
	if noToken.Code != http.StatusForbidden {
		t.Fatalf("logout without csrf = %d", noToken.Code)
	}
	loggedOut := jsonDo(t, h, http.MethodPost, "/dashboard/api/logout", "", cookie[0], view.CSRFToken)
	if loggedOut.Code != http.StatusNoContent {
		t.Fatalf("logout = %d body=%s", loggedOut.Code, loggedOut.Body.String())
	}
}

func TestDenySession_apiIsJSON(t *testing.T) {
	m, _ := newSessionTestManager(t, time.Hour, false)
	h := jsonSessionMux(m)
	rec := jsonDo(t, h, http.MethodGet, "/dashboard/api/session", "", nil, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q", ct)
	}
	if !strings.Contains(rec.Body.String(), `"unauthenticated"`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
	if rec.Header().Get("Location") != "" {
		t.Fatalf("API 401 redirected to %s", rec.Header().Get("Location"))
	}
}

func jsonSessionMux(m *auth.SessionManager) http.Handler {
	r := chi.NewRouter()
	r.Route("/dashboard", func(dash chi.Router) {
		dash.Post("/api/login", m.HandleLoginJSON)
		dash.Group(func(authed chi.Router) {
			authed.Use(m.RequireSession)
			authed.Use(dashboard.WithSessionCSRF)
			authed.Use(middleware.CSRF)
			authed.Get("/api/session", m.HandleSessionJSON)
			authed.Post("/api/logout", m.HandleLogoutJSON)
		})
	})
	return r
}

func jsonPost(t *testing.T, h http.Handler, path, body string, c *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	return jsonDo(t, h, http.MethodPost, path, body, c, "")
}

func jsonDo(t *testing.T, h http.Handler, method, path, body string, c *http.Cookie, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if c != nil {
		req.AddCookie(c)
	}
	if csrf != "" {
		req.Header.Set(middleware.CSRFHeader, csrf)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
