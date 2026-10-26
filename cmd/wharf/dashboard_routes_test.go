package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BRAVO68WEB/wharf/internal/bcrypt"

	"github.com/BRAVO68WEB/wharf/internal/api"
	"github.com/BRAVO68WEB/wharf/internal/config"
	"github.com/BRAVO68WEB/wharf/internal/db"
	"github.com/BRAVO68WEB/wharf/internal/storage/filesystem"
)

func TestDashboardRoutes_spaDoesNotSwallowAPIOrV2(t *testing.T) {
	h, q := testMux(t)

	root := routeGet(t, h, "/")
	if root.Code != http.StatusFound || root.Header().Get("Location") != "/dashboard/" {
		t.Fatalf("GET / = %d location=%s", root.Code, root.Header().Get("Location"))
	}

	index := routeGet(t, h, "/dashboard")
	if index.Code != http.StatusOK || !strings.Contains(index.Body.String(), "Registry dashboard") {
		t.Fatalf("GET /dashboard = %d body=%s", index.Code, index.Body.String())
	}
	page := routeGet(t, h, "/dashboard/repo/library/nginx")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Registry dashboard") {
		t.Fatalf("SPA = %d body=%s", page.Code, page.Body.String())
	}
	asset := routeGet(t, h, "/dashboard/assets/missing.js")
	if asset.Code != http.StatusNotFound || strings.Contains(asset.Body.String(), "Registry dashboard") {
		t.Fatalf("missing asset = %d body=%s", asset.Code, asset.Body.String())
	}

	apiSession := routeGet(t, h, "/dashboard/api/session")
	if apiSession.Code != http.StatusUnauthorized || !strings.Contains(apiSession.Body.String(), "unauthenticated") {
		t.Fatalf("session = %d body=%s", apiSession.Code, apiSession.Body.String())
	}
	if strings.Contains(apiSession.Body.String(), "Registry dashboard") {
		t.Fatal("API session served the SPA")
	}
	unknown := routeGet(t, h, "/dashboard/api/no-such")
	if unknown.Code == http.StatusOK && strings.Contains(unknown.Body.String(), "Registry dashboard") {
		t.Fatal("unknown API path served the SPA")
	}

	v2 := routeGet(t, h, "/v2/")
	if strings.Contains(v2.Body.String(), "Registry dashboard") || v2.Code == http.StatusOK {
		t.Fatalf("/v2/ = %d body=%s", v2.Code, v2.Body.String())
	}
	health := routeGet(t, h, "/healthz")
	if health.Code != http.StatusOK || strings.TrimSpace(health.Body.String()) != "ok" {
		t.Fatalf("healthz = %d body=%s", health.Code, health.Body.String())
	}

	hash, err := bcrypt.GenerateFromPassword([]byte("admin"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateUser(t.Context(), db.CreateUserParams{
		Username: "admin", PasswordHash: string(hash), IsAdmin: true,
	}); err != nil {
		t.Fatal(err)
	}
	login := routeDo(t, h, http.MethodPost, "/dashboard/api/login", `{"username":"admin","password":"admin"}`, nil, "")
	if login.Code != http.StatusOK {
		t.Fatalf("login = %d body=%s", login.Code, login.Body.String())
	}
	form := routeDo(t, h, http.MethodPost, "/dashboard/login", "username=admin&password=admin", nil, "")
	form.Header().Set("Content-Type", "") // recorded after the call; check status only
	if form.Code != http.StatusSeeOther {
		t.Fatalf("form login = %d body=%s location=%s", form.Code, form.Body.String(), form.Header().Get("Location"))
	}
}

func TestDashboardRoutes_publicReposAndPullNeedNoSession(t *testing.T) {
	h, q := testMux(t)
	ctx := t.Context()
	if _, err := q.CreateRepository(ctx, "shown"); err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateRepository(ctx, "hidden"); err != nil {
		t.Fatal(err)
	}
	if _, err := q.SetRepositoryPublished(ctx, db.SetRepositoryPublishedParams{Published: true, Name: "shown"}); err != nil {
		t.Fatal(err)
	}

	page := routeGet(t, h, "/dashboard/api/public/repos")
	if page.Code != http.StatusOK || strings.Contains(page.Body.String(), "Registry dashboard") {
		t.Fatalf("public repos = %d body=%s", page.Code, page.Body.String())
	}
	if !strings.Contains(page.Body.String(), "shown") || strings.Contains(page.Body.String(), "hidden") {
		t.Fatalf("public body = %s", page.Body.String())
	}
	denied := routeDo(t, h, http.MethodPost, "/dashboard/api/repos/publish", `{"name":"hidden","published":true}`, nil, "")
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("publish without session = %d body=%s", denied.Code, denied.Body.String())
	}
	still := routeGet(t, h, "/dashboard/api/public/repos")
	if strings.Contains(still.Body.String(), "hidden") {
		t.Fatalf("unauthenticated publish stuck: %s", still.Body.String())
	}

	pull := routeGet(t, h, "/token?service=registry.docker.io&scope=repository:shown:pull")
	if pull.Code != http.StatusOK || !strings.Contains(pull.Body.String(), `"token"`) {
		t.Fatalf("anonymous pull token = %d body=%s", pull.Code, pull.Body.String())
	}
	priv := routeGet(t, h, "/token?service=registry.docker.io&scope=repository:hidden:pull")
	if priv.Code != http.StatusUnauthorized {
		t.Fatalf("private pull token = %d", priv.Code)
	}
	push := routeGet(t, h, "/token?service=registry.docker.io&scope=repository:shown:push")
	if push.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous push token = %d", push.Code)
	}
}

func testMux(t *testing.T) (http.Handler, db.Querier) {
	t.Helper()
	dir := t.TempDir()
	sqlDB, err := db.OpenAndMigrate(t.Context(), filepath.Join(dir, "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	store, err := filesystem.New(filesystem.Config{Root: filepath.Join(dir, "blobs")})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Auth: config.AuthConfig{
			JWT: config.JWTConfig{
				PrivateKeyPath: filepath.Join(dir, "jwt.pem"),
				Issuer:         "test",
				Service:        "registry.docker.io",
				TokenTTL:       config.Duration(time.Minute),
			},
			Session: config.SessionConfig{
				Secret: "test-session-secret",
				TTL:    config.Duration(time.Hour),
			},
		},
		GC: config.GCConfig{BatchSize: 8},
	}
	h, err := newMux(t.Context(), api.NewHealth(sqlDB, store), store, sqlDB, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return h, db.New(sqlDB)
}

func routeGet(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	return routeDo(t, h, http.MethodGet, path, "", nil, "")
}

func routeDo(t *testing.T, h http.Handler, method, path, body string, c *http.Cookie, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if method == http.MethodPost && strings.HasPrefix(body, "{") {
		req.Header.Set("Content-Type", "application/json")
	}
	if method == http.MethodPost && strings.Contains(body, "username=") {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if c != nil {
		req.AddCookie(c)
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
