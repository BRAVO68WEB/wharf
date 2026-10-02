package dashboard_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/user/better-docker-registry/internal/dashboard"
	"github.com/user/better-docker-registry/internal/db"
)

// tagTestRouter wires the dashboard routes over a real, fresh SQLite database.
func tagTestRouter(t *testing.T) (http.Handler, db.Querier) {
	t.Helper()
	sqlDB, err := db.OpenAndMigrate(t.Context(), filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	q := db.New(sqlDB)

	inner := chi.NewRouter()
	dashboard.NewTagsHandler(q).Mount(inner)
	dashboard.NewManifestsHandler(q).Mount(inner)

	r := chi.NewRouter()
	r.Mount("/dashboard/repos", dashboard.CollapseRepoName(inner))
	return r, q
}

// tagTestSeed creates a repository with one manifest and two tags.
func tagTestSeed(t *testing.T, q db.Querier) db.Repository {
	t.Helper()
	ctx := t.Context()
	repo, err := q.CreateRepository(ctx, "test")
	if err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	if err := q.UpsertManifest(ctx, db.UpsertManifestParams{
		RepoID:    repo.ID,
		Digest:    digest,
		MediaType: "application/vnd.docker.distribution.manifest.v2+json",
		Payload:   []byte(`{"schemaVersion":2,"config":{"digest":"sha256:cfg"},"layers":[]}`),
		Size:      128,
	}); err != nil {
		t.Fatalf("UpsertManifest: %v", err)
	}
	for _, name := range []string{"latest", "v1.0"} {
		if err := q.SetTag(ctx, db.SetTagParams{
			RepoID:         repo.ID,
			Name:           name,
			ManifestDigest: digest,
			UpdatedAt:      time.Now().Unix(),
		}); err != nil {
			t.Fatalf("SetTag %s: %v", name, err)
		}
	}
	return repo
}

// serveGet issues a GET and returns the recorded response.
func tagTestGET(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// --- Tests ---

// Given: no repository named "missing" in the database.
// When:  GET /dashboard/repos/missing.
// Then:  the response is 404.
func Test_listTags_when_repoNotFound_then_404(t *testing.T) {
	h, _ := tagTestRouter(t)
	rec := tagTestGET(t, h, "/dashboard/repos/missing")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// Given: a repo "test" with tags "latest" and "v1.0" pointing to one manifest.
// When:  GET /repos/test.
// Then:  the body has two rows with data-testid="tag-row" and both tag names.
func Test_listTags_when_tagsExist_then_rendersTable(t *testing.T) {
	h, q := tagTestRouter(t)
	tagTestSeed(t, q)

	rec := tagTestGET(t, h, "/dashboard/repos/test")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body)
	}
	body := rec.Body.String()
	if got := strings.Count(body, `data-testid="tag-row"`); got != 2 {
		t.Fatalf("tag-row count = %d, want 2", got)
	}
	for _, name := range []string{"latest", "v1.0"} {
		if !strings.Contains(body, name) {
			t.Errorf("body missing tag name %q", name)
		}
	}
	if !strings.Contains(body, "sha256:"+strings.Repeat("a", 64)) {
		t.Error("body missing manifest digest")
	}
	if !strings.Contains(body, "128 B") {
		t.Error("body missing manifest size")
	}
	// The hx-get attributes must point at the manifest detail route and
	// target the #manifest-detail container.
	if !strings.Contains(body, `hx-target="#manifest-detail"`) {
		t.Error("body missing hx-target=#manifest-detail")
	}
	if !strings.Contains(body, "/manifests/") {
		t.Error("body missing hx-get link to manifest detail")
	}
}

// Given: a repo "empty" with no tags.
// When:  GET /repos/empty.
// Then:  the body shows the empty-state message and no tag rows.
func Test_listTags_when_noTags_then_emptyState(t *testing.T) {
	h, q := tagTestRouter(t)
	if _, err := q.CreateRepository(t.Context(), "empty"); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	rec := tagTestGET(t, h, "/dashboard/repos/empty")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "No tags") {
		t.Error("body missing empty-state message")
	}
	if strings.Contains(body, `data-testid="tag-row"`) {
		t.Error("body has tag rows for an empty repo")
	}
}

// Given: a repo "test" with tags.
// When:  GET /repos/test with HX-Request: true.
// Then:  the response is a fragment (no <html>), containing the tag table.
func Test_listTags_when_htmxRequest_then_fragment(t *testing.T) {
	h, q := tagTestRouter(t)
	tagTestSeed(t, q)

	req := httptest.NewRequest(http.MethodGet, "/dashboard/repos/test", nil)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if strings.Contains(body, "<html") {
		t.Error("HTMX fragment should not contain <html>")
	}
	if !strings.Contains(body, `data-testid="tag-row"`) {
		t.Error("fragment missing tag rows")
	}
}

// Given: a repo "library/nginx" with one tag.
// When:  GET /repos/library/nginx (multi-segment name).
// Then:  the response is 200 and contains the tag row.
func Test_listTags_when_multisegmentRepo_then_resolvesName(t *testing.T) {
	h, q := tagTestRouter(t)
	ctx := t.Context()
	repo, err := q.CreateRepository(ctx, "library/nginx")
	if err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}
	digest := "sha256:" + strings.Repeat("b", 64)
	if err := q.UpsertManifest(ctx, db.UpsertManifestParams{
		RepoID:    repo.ID,
		Digest:    digest,
		MediaType: "application/vnd.docker.distribution.manifest.v2+json",
		Payload:   []byte(`{"schemaVersion":2,"config":{"digest":"sha256:x"},"layers":[]}`),
		Size:      64,
	}); err != nil {
		t.Fatalf("UpsertManifest: %v", err)
	}
	if err := q.SetTag(ctx, db.SetTagParams{
		RepoID:         repo.ID,
		Name:           "latest",
		ManifestDigest: digest,
		UpdatedAt:      time.Now().Unix(),
	}); err != nil {
		t.Fatalf("SetTag: %v", err)
	}

	rec := tagTestGET(t, h, "/dashboard/repos/library/nginx")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `data-testid="tag-row"`) {
		t.Error("body missing tag row for multi-segment repo")
	}
}

// formatBytes table test.
func Test_formatBytes_when_variousSizes_then_humanReadable(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{-1, "—"},
	}
	for _, c := range cases {
		if got := dashboard.FormatBytes(c.n); got != c.want {
			t.Errorf("FormatBytes(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}
