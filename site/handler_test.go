package site

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandler_servesIndexAndFallsBack(t *testing.T) {
	h := Handler()

	index := get(t, h, "/dashboard")
	if index.Code != http.StatusOK {
		t.Fatalf("GET /dashboard = %d", index.Code)
	}
	if !strings.Contains(index.Body.String(), "Registry dashboard") {
		t.Fatalf("index body = %q", index.Body.String())
	}
	if cc := index.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("index Cache-Control = %q, want no-store", cc)
	}

	fallback := get(t, h, "/dashboard/repo/library/nginx")
	if fallback.Code != http.StatusOK {
		t.Fatalf("SPA fallback = %d", fallback.Code)
	}
	if !strings.Contains(fallback.Body.String(), "Registry dashboard") {
		t.Fatalf("fallback body = %q", fallback.Body.String())
	}
}

func TestHandler_missingAssetIs404(t *testing.T) {
	h := Handler()
	rec := get(t, h, "/dashboard/assets/missing.js")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "Registry dashboard") {
		t.Fatal("missing asset fell back to index.html")
	}
}

func TestHandler_doesNotSwallowAPI(t *testing.T) {
	h := Handler()
	for _, path := range []string{"/dashboard/api", "/dashboard/api/repos", "/dashboard/api/session"} {
		rec := get(t, h, path)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "Registry dashboard") {
			t.Errorf("GET %s served the SPA", path)
		}
	}
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
