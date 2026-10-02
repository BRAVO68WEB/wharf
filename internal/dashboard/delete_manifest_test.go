package dashboard_test

import (
	"database/sql"
	"errors"
	"html"
	"net/http"
	"strings"
	"testing"

	"github.com/user/better-docker-registry/internal/db"
	"github.com/user/better-docker-registry/internal/middleware"
)

// Manifest delete flow (plan task 17): the manifest row and every tag pointing
// at it go together, blobs do not, and the manifest detail swaps out on HTMX.

// Given: repo "test" with one manifest carrying tags "latest" and "v1.0".
// When:  DELETE the manifest by digest as an HTMX request.
// Then:  an empty 200, the manifest is gone and so is every tag on it.
func Test_deleteManifest_when_htmx_then_manifestAndAllTagsRemoved(t *testing.T) {
	h, q := deleteTestRouter(t)
	repo := tagTestSeed(t, q)
	digest := "sha256:" + strings.Repeat("a", 64)

	rec := deleteTestDELETE(t, h, "/dashboard/repos/test/manifests/"+digest, map[string]string{
		middleware.CSRFHeader: gcTestCSRFToken,
		"HX-Request":          "true",
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if body := rec.Body.String(); strings.TrimSpace(body) != "" {
		t.Errorf("HTMX body = %q, want empty for the outerHTML panel swap", body)
	}

	ctx := t.Context()
	if _, err := q.GetManifest(ctx, db.GetManifestParams{RepoID: repo.ID, Digest: digest}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetManifest err = %v, want sql.ErrNoRows", err)
	}
	for _, name := range []string{"latest", "v1.0"} {
		if _, err := q.GetTag(ctx, db.GetTagParams{RepoID: repo.ID, Name: name}); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("GetTag(%s) err = %v, want sql.ErrNoRows", name, err)
		}
	}
}

// Given: the same fixture, a plain (non-HTMX) caller.
// When:  DELETE the manifest by digest.
// Then:  303 back to the repository's tag list.
func Test_deleteManifest_when_plainRequest_then_redirectsToRepo(t *testing.T) {
	h, q := deleteTestRouter(t)
	tagTestSeed(t, q)
	digest := "sha256:" + strings.Repeat("a", 64)

	rec := deleteTestDELETE(t, h, "/dashboard/repos/test/manifests/"+digest, map[string]string{
		middleware.CSRFHeader: gcTestCSRFToken,
	})

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body=%s", rec.Code, rec.Body)
	}
	if loc := rec.Header().Get("Location"); loc != "/dashboard/repos/test" {
		t.Errorf("Location = %q, want /dashboard/repos/test", loc)
	}
}

// Given: a repo with one manifest.
// When:  DELETE names an unknown digest or an unknown repo.
// Then:  404 and the existing rows are untouched.
func Test_deleteManifest_when_unknown_then_notFound(t *testing.T) {
	h, q := deleteTestRouter(t)
	repo := tagTestSeed(t, q)

	for _, path := range []string{
		"/dashboard/repos/test/manifests/sha256:" + strings.Repeat("f", 64),
		"/dashboard/repos/missing/manifests/sha256:" + strings.Repeat("a", 64),
	} {
		rec := deleteTestDELETE(t, h, path, map[string]string{
			middleware.CSRFHeader: gcTestCSRFToken,
			"HX-Request":          "true",
		})
		if rec.Code != http.StatusNotFound {
			t.Errorf("DELETE %s: status = %d, want 404", path, rec.Code)
		}
	}

	if _, err := q.GetManifest(t.Context(), db.GetManifestParams{
		RepoID: repo.ID,
		Digest: "sha256:" + strings.Repeat("a", 64),
	}); err != nil {
		t.Errorf("manifest must survive the unknown-target deletes: %v", err)
	}
}

// Given: the manifest detail for "test".
// When:  it is rendered with a bound CSRF token.
// Then:  it carries a delete-manifest-btn with the hx-delete / hx-confirm /
// hx-swap contract and the CSRF token in hx-headers.
func Test_manifestDetail_when_rendered_then_deleteManifestButton(t *testing.T) {
	h, q := deleteTestRouter(t)
	tagTestSeed(t, q)
	digest := "sha256:" + strings.Repeat("a", 64)

	rec := tagTestGET(t, h, "/dashboard/repos/test/manifests/"+digest)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := html.UnescapeString(rec.Body.String())

	if got := strings.Count(body, `data-testid="delete-manifest-btn"`); got != 1 {
		t.Fatalf("delete-manifest-btn count = %d, want 1", got)
	}
	for _, want := range []string{
		`hx-delete="/dashboard/repos/test/manifests/` + digest + `"`,
		`hx-confirm="Delete manifest ` + digest + `?"`,
		`hx-swap="outerHTML"`,
		`data-testid="manifest-panel"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("manifest detail missing %q", want)
		}
	}
	if token := repoTestCSRFToken(t, body); token != gcTestCSRFToken {
		t.Errorf("hx-headers token = %q, want %q", token, gcTestCSRFToken)
	}
}
