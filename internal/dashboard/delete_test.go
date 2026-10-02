package dashboard_test

import (
	"database/sql"
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/user/better-docker-registry/internal/dashboard"
	"github.com/user/better-docker-registry/internal/db"
	"github.com/user/better-docker-registry/internal/middleware"
)

// Delete-flow tests (plan task 17): a tag delete removes only the tag
// reference and leaves the manifest alone, a manifest delete takes the
// manifest and every tag with it, both answer HTMX with an empty outerHTML
// swap and plain callers with a 303 back to the repo, and every request goes
// through middleware.CSRF. Helpers are deleteTest-prefixed; the CSRF binding
// wrapper is the GC suite's gcTestWithCSRF (same package).

// deleteTestRouter wires the tag, manifest and delete routes over a fresh
// database behind middleware.CSRF — the mount the integrating task uses.
func deleteTestRouter(t *testing.T) (http.Handler, db.Querier) {
	t.Helper()
	q := repoTestDB(t)
	inner := chi.NewRouter()
	dashboard.NewTagsHandler(q).Mount(inner)
	dashboard.NewManifestsHandler(q).Mount(inner)
	dashboard.NewDeleteHandler(q).Mount(inner)
	r := chi.NewRouter()
	r.Mount("/dashboard/repos", dashboard.CollapseRepoName(inner))
	return gcTestWithCSRF(r), q
}

// deleteTestDELETE issues one DELETE with the given headers. The caller
// passes middleware.CSRFHeader explicitly so the refusals are as visible as
// the successes.
func deleteTestDELETE(t *testing.T, h http.Handler, path string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, path, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Given: repo "test" with tags "latest" and "v1.0" on one manifest.
// When:  DELETE /dashboard/repos/test/tags/latest as an HTMX request.
// Then:  an empty 200 (the outerHTML swap drops the row), the tag is gone,
// and the manifest plus the sibling tag survive.
func Test_deleteTag_when_htmx_then_onlyThatTagRemoved(t *testing.T) {
	h, q := deleteTestRouter(t)
	repo := tagTestSeed(t, q)

	rec := deleteTestDELETE(t, h, "/dashboard/repos/test/tags/latest", map[string]string{
		middleware.CSRFHeader: gcTestCSRFToken,
		"HX-Request":          "true",
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if body := rec.Body.String(); strings.TrimSpace(body) != "" {
		t.Errorf("HTMX body = %q, want empty for the outerHTML row swap", body)
	}

	ctx := t.Context()
	if _, err := q.GetTag(ctx, db.GetTagParams{RepoID: repo.ID, Name: "latest"}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetTag(latest) err = %v, want sql.ErrNoRows", err)
	}
	if _, err := q.GetTag(ctx, db.GetTagParams{RepoID: repo.ID, Name: "v1.0"}); err != nil {
		t.Errorf("sibling tag v1.0 must survive: %v", err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	if _, err := q.GetManifest(ctx, db.GetManifestParams{RepoID: repo.ID, Digest: digest}); err != nil {
		t.Errorf("manifest must survive a tag delete: %v", err)
	}
}

// Given: repo "test" with tags, a plain (non-HTMX) caller.
// When:  DELETE /dashboard/repos/test/tags/v1.0.
// Then:  303 back to the repository's tag list.
func Test_deleteTag_when_plainRequest_then_redirectsToRepo(t *testing.T) {
	h, q := deleteTestRouter(t)
	tagTestSeed(t, q)

	rec := deleteTestDELETE(t, h, "/dashboard/repos/test/tags/v1.0", map[string]string{
		middleware.CSRFHeader: gcTestCSRFToken,
	})

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body=%s", rec.Code, rec.Body)
	}
	if loc := rec.Header().Get("Location"); loc != "/dashboard/repos/test" {
		t.Errorf("Location = %q, want /dashboard/repos/test", loc)
	}
}

// Given: repo "test" whose manifest has the single tag "latest".
// When:  that tag is deleted.
// Then:  the manifest row survives: a tag delete unmaps a name only (OCI
// semantics, like `docker rmi name:tag`); the digest stays resolvable and
// removing the manifest is the manifest DELETE route's job.
func Test_deleteTag_when_lastTag_then_manifestSurvives(t *testing.T) {
	h, q := deleteTestRouter(t)
	digest := manifestTestSeed(t, q, "test", manifestTestImagePayload,
		"application/vnd.docker.distribution.manifest.v2+json")

	rec := deleteTestDELETE(t, h, "/dashboard/repos/test/tags/latest", map[string]string{
		middleware.CSRFHeader: gcTestCSRFToken,
		"HX-Request":          "true",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	ctx := t.Context()
	repo, err := q.GetRepositoryByName(ctx, "test")
	if err != nil {
		t.Fatalf("GetRepositoryByName: %v", err)
	}
	if _, err := q.GetTag(ctx, db.GetTagParams{RepoID: repo.ID, Name: "latest"}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetTag(latest) err = %v, want sql.ErrNoRows", err)
	}
	if _, err := q.GetManifest(ctx, db.GetManifestParams{RepoID: repo.ID, Digest: digest}); err != nil {
		t.Errorf("manifest %s must survive its last tag delete: %v", digest, err)
	}
}

// Given: a repo with a tag, and requests naming a missing repo or tag.
// When:  DELETE for "missing/nope" and for "test/ghost".
// Then:  404 for both, and nothing is deleted.
func Test_deleteTag_when_unknown_then_notFound(t *testing.T) {
	h, q := deleteTestRouter(t)
	repo := tagTestSeed(t, q)

	for _, path := range []string{
		"/dashboard/repos/missing/nope/tags/latest",
		"/dashboard/repos/test/tags/ghost",
	} {
		rec := deleteTestDELETE(t, h, path, map[string]string{
			middleware.CSRFHeader: gcTestCSRFToken,
			"HX-Request":          "true",
		})
		if rec.Code != http.StatusNotFound {
			t.Errorf("DELETE %s: status = %d, want 404", path, rec.Code)
		}
	}

	if _, err := q.GetTag(t.Context(), db.GetTagParams{RepoID: repo.ID, Name: "latest"}); err != nil {
		t.Errorf("tag latest must survive the unknown-target deletes: %v", err)
	}
}

// Given: the multi-segment repo "library/nginx" with a tag.
// When:  DELETE /dashboard/repos/library/nginx/tags/latest.
// Then:  the collapsed name routes to the handler and the tag is gone.
func Test_deleteTag_when_multisegmentRepo_then_routed(t *testing.T) {
	h, q := deleteTestRouter(t)
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
		Payload:   []byte(`{"schemaVersion":2}`),
		Size:      64,
	}); err != nil {
		t.Fatalf("UpsertManifest: %v", err)
	}
	if err := q.SetTag(ctx, db.SetTagParams{
		RepoID:         repo.ID,
		Name:           "latest",
		ManifestDigest: digest,
		UpdatedAt:      1,
	}); err != nil {
		t.Fatalf("SetTag: %v", err)
	}

	rec := deleteTestDELETE(t, h, "/dashboard/repos/library/nginx/tags/latest", map[string]string{
		middleware.CSRFHeader: gcTestCSRFToken,
		"HX-Request":          "true",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if _, err := q.GetTag(ctx, db.GetTagParams{RepoID: repo.ID, Name: "latest"}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetTag err = %v, want sql.ErrNoRows", err)
	}
}

// Given: the tag list page for "test" with a bound CSRF token.
// When:  it is rendered.
// Then:  every row carries a delete-tag-btn with the hx-delete / hx-confirm /
// hx-swap contract and the CSRF token in hx-headers.
func Test_tagList_when_rendered_then_deleteTagButtons(t *testing.T) {
	h, q := deleteTestRouter(t)
	tagTestSeed(t, q)

	rec := tagTestGET(t, h, "/dashboard/repos/test")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := html.UnescapeString(rec.Body.String())

	if got := strings.Count(body, `data-testid="delete-tag-btn"`); got != 2 {
		t.Fatalf("delete-tag-btn count = %d, want 2", got)
	}
	for _, want := range []string{
		`hx-delete="/dashboard/repos/test/tags/latest"`,
		`hx-delete="/dashboard/repos/test/tags/v1.0"`,
		`hx-confirm="Delete tag latest?"`,
		`hx-target="closest tr"`,
		`hx-swap="outerHTML"`,
		`hx-trigger="click consume"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("tag list missing %q", want)
		}
	}
	if token := repoTestCSRFToken(t, body); token != gcTestCSRFToken {
		t.Errorf("hx-headers token = %q, want %q", token, gcTestCSRFToken)
	}
}
