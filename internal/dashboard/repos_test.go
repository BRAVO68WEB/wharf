package dashboard_test

import (
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/user/better-docker-registry/internal/dashboard"
	"github.com/user/better-docker-registry/internal/db"
)

// Helpers are repoTest-prefixed: package dashboard_test is shared with the
// tag/manifest tests landing in this wave. Test files split by concern —
// browse page (this one), filter, pager, session/CSRF wiring.

// repoTestDB opens and migrates a throwaway database for one test.
func repoTestDB(t *testing.T) db.Querier {
	t.Helper()
	sqlDB, err := db.OpenAndMigrate(t.Context(), filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db.New(sqlDB)
}

// repoTestRouter wires the repository browse routes over a fresh database.
func repoTestRouter(t *testing.T) (http.Handler, db.Querier) {
	t.Helper()
	q := repoTestDB(t)
	r := chi.NewRouter()
	dashboard.NewRepoHandler(q).Mount(r)
	return r, q
}

// repoTestSeed creates one repository carrying `manifests` manifests and
// `tags` tags. Tags are spread over the manifests so the tag foreign key holds.
func repoTestSeed(t *testing.T, q db.Querier, name string, manifests, tags int) {
	t.Helper()
	ctx := t.Context()
	repo, err := q.CreateRepository(ctx, name)
	if err != nil {
		t.Fatalf("CreateRepository %s: %v", name, err)
	}
	digests := make([]string, 0, manifests)
	for i := range manifests {
		digest := fmt.Sprintf("sha256:%064x", i+1)
		if err := q.UpsertManifest(ctx, db.UpsertManifestParams{
			RepoID:    repo.ID,
			Digest:    digest,
			MediaType: "application/vnd.docker.distribution.manifest.v2+json",
			Payload:   []byte(`{"schemaVersion":2}`),
			Size:      int64(100 + i),
		}); err != nil {
			t.Fatalf("UpsertManifest %s: %v", name, err)
		}
		digests = append(digests, digest)
	}
	for i := range tags {
		if err := q.SetTag(ctx, db.SetTagParams{
			RepoID:         repo.ID,
			Name:           fmt.Sprintf("t%d", i),
			ManifestDigest: digests[i%len(digests)],
			UpdatedAt:      time.Now().Unix(),
		}); err != nil {
			t.Fatalf("SetTag %s: %v", name, err)
		}
	}
}

// repoTestGET issues a GET and returns the recorded response.
func repoTestGET(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// repoTestID counts occurrences of a data-testid attribute in a rendered
// page — the Playwright contract these templates are written against.
func repoTestID(body, testID string) int {
	return strings.Count(body, `data-testid="`+testID+`"`)
}

// repoTestCSRFToken pulls the session token back out of the rendered
// hx-headers attribute (templ HTML-escapes attribute values).
func repoTestCSRFToken(t *testing.T, body string) string {
	t.Helper()
	match := regexp.MustCompile(`"X-CSRF-Token"\s*:\s*"([^"]*)"`).FindStringSubmatch(html.UnescapeString(body))
	if match == nil {
		return ""
	}
	return match[1]
}

// Given: the registry has no repositories.
// When:  GET /dashboard.
// Then:  the page renders the empty state instead of a table.
func Test_listRepos_when_noRepositories_then_emptyState(t *testing.T) {
	h, _ := repoTestRouter(t)

	rec := repoTestGET(t, h, "/dashboard")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "No repositories found.") {
		t.Errorf("body missing empty-state message")
	}
	if got := repoTestID(body, "repo-empty"); got != 1 {
		t.Errorf(`data-testid="repo-empty" count = %d, want 1`, got)
	}
	if got := repoTestID(body, "repo-row"); got != 0 {
		t.Errorf(`data-testid="repo-row" count = %d, want 0`, got)
	}
}

// Given: three repositories, one of them with a multi-segment name.
// When:  GET /dashboard.
// Then:  one row per repository, marked with data-testid="repo-row".
func Test_listRepos_when_repositories_then_tableRows(t *testing.T) {
	h, q := repoTestRouter(t)
	repoTestSeed(t, q, "alpha", 2, 3)
	repoTestSeed(t, q, "library/nginx", 1, 1)
	repoTestSeed(t, q, "zeta", 0, 0)

	rec := repoTestGET(t, h, "/dashboard")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if got := repoTestID(body, "repo-row"); got != 3 {
		t.Fatalf(`data-testid="repo-row" count = %d, want 3`, got)
	}
	for _, name := range []string{"alpha", "library/nginx", "zeta"} {
		if !strings.Contains(body, `data-repo="`+name+`"`) {
			t.Errorf("body missing row for repository %q", name)
		}
	}
}

// Given: a repository exists.
// When:  GET /dashboard.
// Then:  the response carries the HTMX shell and the three nav entries.
func Test_listRepos_when_rendered_then_htmxShellAndNav(t *testing.T) {
	h, q := repoTestRouter(t)
	repoTestSeed(t, q, "alpha", 1, 1)

	body := repoTestGET(t, h, "/dashboard").Body.String()

	if !strings.Contains(body, "https://unpkg.com/htmx.org@1.9.10") {
		t.Errorf("body missing the pinned HTMX runtime script tag")
	}
	for _, id := range []string{"nav-repos", "nav-gc", "nav-logout"} {
		if got := repoTestID(body, id); got != 1 {
			t.Errorf(`data-testid=%q count = %d, want 1`, id, got)
		}
	}
	if !strings.Contains(body, `hx-get="/dashboard/repos"`) {
		t.Errorf("body missing the live-search hx-get on the filter input")
	}
	if !strings.Contains(body, `name="search"`) {
		t.Errorf("body missing the search input's name attribute")
	}
	if !strings.Contains(body, `hx-headers=`) {
		t.Errorf("body missing hx-headers with the CSRF token")
	}
}
