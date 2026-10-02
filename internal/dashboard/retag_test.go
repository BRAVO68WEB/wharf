package dashboard_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/user/better-docker-registry/internal/dashboard"
	"github.com/user/better-docker-registry/internal/db"
	"github.com/user/better-docker-registry/internal/middleware"
)

// Retag tests (plan task 21): the retag form copies one tag's manifest to a
// new tag name and leaves the original tag untouched. Helpers are
// retagTest-prefixed; the suite reuses the delete/GC helpers
// (repoTestDB, tagTestSeed, gcTestWithCSRF, gcTestCSRFToken,
// deleteTestAuditRows) instead of re-implementing them.

// retagTestRouter wires the tag, manifest, delete and retag routes over a
// fresh database behind middleware.CSRF — the mount the integrating task
// uses.
func retagTestRouter(t *testing.T) (http.Handler, db.Querier) {
	t.Helper()
	q := repoTestDB(t)
	inner := chi.NewRouter()
	dashboard.NewTagsHandler(q).Mount(inner)
	dashboard.NewManifestsHandler(q).Mount(inner)
	dashboard.NewDeleteHandler(q).Mount(inner)
	dashboard.NewRetagHandler(q).Mount(inner)
	r := chi.NewRouter()
	r.Mount("/dashboard/repos", dashboard.CollapseRepoName(inner))
	return gcTestWithCSRF(r), q
}

// retagTestPOST issues one form POST with the given fields and headers. The
// caller passes middleware.CSRFHeader explicitly so the refusals are as
// visible as the successes.
func retagTestPOST(t *testing.T, h http.Handler, path string, form, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	values := url.Values{}
	for k, v := range form {
		values.Set(k, v)
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Given: the tag list page for "test" with a bound CSRF token.
// When:  it is rendered.
// Then:  each row carries a retag-btn that loads the form into the one
// retag-modal container, consuming its click like the delete button.
func Test_tagList_when_rendered_then_retagButtonsAndModal(t *testing.T) {
	h, q := retagTestRouter(t)
	tagTestSeed(t, q)

	rec := tagTestGET(t, h, "/dashboard/repos/test")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	if got := repoTestID(body, "retag-btn"); got != 2 {
		t.Fatalf("retag-btn count = %d, want 2", got)
	}
	if got := repoTestID(body, "retag-modal"); got != 1 {
		t.Fatalf("retag-modal count = %d, want 1", got)
	}
	for _, want := range []string{
		`hx-get="/dashboard/repos/test/tags/latest/retag"`,
		`hx-get="/dashboard/repos/test/tags/v1.0/retag"`,
		`hx-target="#retag-modal"`,
		`hx-swap="innerHTML"`,
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

// Given: repo "test" with the tag "latest".
// When:  GET the retag form URL for that tag.
// Then:  the form carries the new-tag and target-repo inputs, posts back to
// the same URL with the CSRF token, and defaults target_repo to this repo.
func Test_retagForm_when_get_then_rendersFields(t *testing.T) {
	h, q := retagTestRouter(t)
	tagTestSeed(t, q)

	rec := tagTestGET(t, h, "/dashboard/repos/test/tags/latest/retag")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	body := rec.Body.String()

	if repoTestID(body, "retag-form") != 1 {
		t.Fatalf("retag-form count = %d, want 1", repoTestID(body, "retag-form"))
	}
	for _, want := range []string{
		`data-testid="retag-new-tag"`,
		`name="new_tag"`,
		`data-testid="retag-target-repo"`,
		`name="target_repo"`,
		`value="test"`,
		`data-testid="retag-submit"`,
		`method="post"`,
		`hx-post="/dashboard/repos/test/tags/latest/retag"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("retag form missing %q", want)
		}
	}
	if token := repoTestCSRFToken(t, body); token != gcTestCSRFToken {
		t.Errorf("hx-headers token = %q, want %q", token, gcTestCSRFToken)
	}
}

// Given: repo "test" with tags "latest" and "v1.0" on one manifest.
// When:  retagging "latest" to "v2.0" in the same repo as an HTMX request.
// Then:  HX-Redirect points at the tag list, "v2.0" points at the same
// manifest, and the original tags are untouched (copy, not move).
func Test_retag_when_htmxPost_then_newTagCopiedAndOriginalKept(t *testing.T) {
	h, q := retagTestRouter(t)
	repo := tagTestSeed(t, q)
	digest := "sha256:" + strings.Repeat("a", 64)

	rec := retagTestPOST(t, h, "/dashboard/repos/test/tags/latest/retag",
		map[string]string{"new_tag": "v2.0", "target_repo": "test"},
		map[string]string{middleware.CSRFHeader: gcTestCSRFToken, "HX-Request": "true"},
	)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body=%s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("HX-Redirect"); got != "/dashboard/repos/test" {
		t.Errorf("HX-Redirect = %q, want /dashboard/repos/test", got)
	}

	ctx := t.Context()
	tag, err := q.GetTag(ctx, db.GetTagParams{RepoID: repo.ID, Name: "v2.0"})
	if err != nil {
		t.Fatalf("GetTag(v2.0): %v", err)
	}
	if tag.ManifestDigest != digest {
		t.Errorf("v2.0 digest = %q, want %q", tag.ManifestDigest, digest)
	}
	for _, name := range []string{"latest", "v1.0"} {
		tag, err := q.GetTag(ctx, db.GetTagParams{RepoID: repo.ID, Name: name})
		if err != nil {
			t.Fatalf("GetTag(%s): %v", name, err)
		}
		if tag.ManifestDigest != digest {
			t.Errorf("%s digest = %q, want %q (original must not move)", name, tag.ManifestDigest, digest)
		}
	}
}

// Given: a plain (non-HTMX) caller submitting the retag form.
// When:  the retag completes.
// Then:  303 Location points at the repository's tag list.
func Test_retag_when_plainPost_then_seeOtherToTagList(t *testing.T) {
	h, q := retagTestRouter(t)
	tagTestSeed(t, q)

	rec := retagTestPOST(t, h, "/dashboard/repos/test/tags/latest/retag",
		map[string]string{"new_tag": "v2.0", "target_repo": "test"},
		map[string]string{middleware.CSRFHeader: gcTestCSRFToken},
	)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body=%s", rec.Code, rec.Body)
	}
	if loc := rec.Header().Get("Location"); loc != "/dashboard/repos/test" {
		t.Errorf("Location = %q, want /dashboard/repos/test", loc)
	}
}

// Given: the multi-segment repo "library/nginx" with a tag.
// When:  retagging to "v2.0" with no target repo (defaults to the source).
// Then:  the collapsed name routes to the handler and the new tag lands in
// "library/nginx".
func Test_retag_when_multisegmentRepo_then_routed(t *testing.T) {
	h, q := retagTestRouter(t)
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

	rec := retagTestPOST(t, h, "/dashboard/repos/library/nginx/tags/latest/retag",
		map[string]string{"new_tag": "v2.0"},
		map[string]string{middleware.CSRFHeader: gcTestCSRFToken, "HX-Request": "true"},
	)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body=%s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("HX-Redirect"); got != "/dashboard/repos/library/nginx" {
		t.Errorf("HX-Redirect = %q, want /dashboard/repos/library/nginx", got)
	}
	if _, err := q.GetTag(ctx, db.GetTagParams{RepoID: repo.ID, Name: "v2.0"}); err != nil {
		t.Errorf("GetTag(v2.0): %v", err)
	}
}
