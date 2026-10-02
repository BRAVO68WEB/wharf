package dashboard_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/user/better-docker-registry/internal/db"
	"github.com/user/better-docker-registry/internal/middleware"
)

// CSRF + audit + input-validation coverage for the retag flow (plan task 21):
// POST without X-CSRF-Token is refused before anything is written, every
// completed retag lands in audit_log, and malformed form input comes back as
// a fixable form error instead of a half-applied copy.

// Given: the retag route behind middleware.CSRF with a bound token.
// When:  POST arrives without the X-CSRF-Token header.
// Then:  403 and no tag is created.
func Test_retag_when_noCSRFToken_then_403(t *testing.T) {
	h, q := retagTestRouter(t)
	repo := tagTestSeed(t, q)

	rec := retagTestPOST(t, h, "/dashboard/repos/test/tags/latest/retag",
		map[string]string{"new_tag": "v2.0"},
		map[string]string{"HX-Request": "true"},
	)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body)
	}
	if _, err := q.GetTag(t.Context(), db.GetTagParams{RepoID: repo.ID, Name: "v2.0"}); err == nil {
		t.Error("tag v2.0 must not exist after a CSRF refusal")
	}
}

// Given: a completed retag through the plain router (no session).
// When:  it lands.
// Then:  audit_log gains one row: action retag_tag, target
// "test:latest->test:v2.0", and no user id.
func Test_retag_when_completed_then_auditRowWritten(t *testing.T) {
	h, q := retagTestRouter(t)
	tagTestSeed(t, q)

	rec := retagTestPOST(t, h, "/dashboard/repos/test/tags/latest/retag",
		map[string]string{"new_tag": "v2.0", "target_repo": "test"},
		map[string]string{middleware.CSRFHeader: gcTestCSRFToken, "HX-Request": "true"},
	)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body=%s", rec.Code, rec.Body)
	}

	rows := deleteTestAuditRows(t, q)
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.Action != "retag_tag" {
		t.Errorf("action = %q, want retag_tag", row.Action)
	}
	if row.Target != "test:latest->test:v2.0" {
		t.Errorf("target = %q, want test:latest->test:v2.0", row.Target)
	}
	if row.UserID.Valid {
		t.Errorf("user id = %d, want NULL for a session-less retag", row.UserID.Int64)
	}
	if row.CreatedAt <= 0 {
		t.Errorf("created_at = %d, want a unix timestamp", row.CreatedAt)
	}
}

// Given: an HTMX retag with a new tag name outside the OCI tag grammar.
// When:  the form is submitted.
// Then:  the form is rendered back with a retag-error message and nothing is
// copied (HTMX swaps only 2xx, so the answer is a 200).
func Test_retag_when_invalidNewTag_then_formWithError(t *testing.T) {
	h, q := retagTestRouter(t)
	repo := tagTestSeed(t, q)

	rec := retagTestPOST(t, h, "/dashboard/repos/test/tags/latest/retag",
		map[string]string{"new_tag": "bad!tag", "target_repo": "test"},
		map[string]string{middleware.CSRFHeader: gcTestCSRFToken, "HX-Request": "true"},
	)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if repoTestID(body, "retag-error") != 1 {
		t.Errorf("retag-error count = %d, want 1; body=%s", repoTestID(body, "retag-error"), body)
	}
	if repoTestID(body, "retag-form") != 1 {
		t.Errorf("form must be rendered back for a fix, body=%s", body)
	}
	if _, err := q.GetTag(t.Context(), db.GetTagParams{RepoID: repo.ID, Name: "bad!tag"}); err == nil {
		t.Error("invalid tag must not be created")
	}
}

// Given: a plain (non-HTMX) retag with the same invalid tag name.
// When:  the form is submitted.
// Then:  400 with the reason, and nothing is copied.
func Test_retag_when_invalidNewTagAndPlainPost_then_400(t *testing.T) {
	h, q := retagTestRouter(t)
	repo := tagTestSeed(t, q)

	rec := retagTestPOST(t, h, "/dashboard/repos/test/tags/latest/retag",
		map[string]string{"new_tag": "bad!tag"},
		map[string]string{middleware.CSRFHeader: gcTestCSRFToken},
	)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body)
	}
	if _, err := q.GetTag(t.Context(), db.GetTagParams{RepoID: repo.ID, Name: "bad!tag"}); err == nil {
		t.Error("invalid tag must not be created")
	}
}

// Given: a retag naming a target repository outside the OCI name grammar.
// When:  the form is submitted.
// Then:  the form is rendered back with a retag-error and nothing is copied.
func Test_retag_when_invalidTargetRepo_then_formWithError(t *testing.T) {
	h, q := retagTestRouter(t)
	repo := tagTestSeed(t, q)

	rec := retagTestPOST(t, h, "/dashboard/repos/test/tags/latest/retag",
		map[string]string{"new_tag": "v2.0", "target_repo": "Bad/Name"},
		map[string]string{middleware.CSRFHeader: gcTestCSRFToken, "HX-Request": "true"},
	)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if repoTestID(rec.Body.String(), "retag-error") != 1 {
		t.Errorf("retag-error count = %d, want 1", repoTestID(rec.Body.String(), "retag-error"))
	}
	if _, err := q.GetTag(t.Context(), db.GetTagParams{RepoID: repo.ID, Name: "v2.0"}); err == nil {
		t.Error("tag v2.0 must not be created for an invalid target repo")
	}
}

// Given: a retag naming a missing repo or a missing source tag.
// When:  the form is submitted.
// Then:  404 and nothing is created.
func Test_retag_when_unknownSource_then_notFound(t *testing.T) {
	h, q := retagTestRouter(t)
	tagTestSeed(t, q)

	for _, path := range []string{
		"/dashboard/repos/missing/nope/tags/latest/retag",
		"/dashboard/repos/test/tags/ghost/retag",
	} {
		rec := retagTestPOST(t, h, path,
			map[string]string{"new_tag": "v2.0"},
			map[string]string{middleware.CSRFHeader: gcTestCSRFToken, "HX-Request": "true"},
		)
		if rec.Code != http.StatusNotFound {
			t.Errorf("POST %s: status = %d, want 404", path, rec.Code)
		}
	}

	ctx := t.Context()
	if rows := deleteTestAuditRows(t, q); len(rows) != 0 {
		t.Errorf("audit rows = %d, want 0 for refused retags", len(rows))
	}
	repo, err := q.GetRepositoryByName(ctx, "test")
	if err != nil {
		t.Fatalf("GetRepositoryByName: %v", err)
	}
	if _, err := q.GetTag(ctx, db.GetTagParams{RepoID: repo.ID, Name: "v2.0"}); err == nil {
		t.Error("tag v2.0 must not be created")
	}
}

// Given: the retag form URL for a tag that does not exist.
// When:  GET requests the form.
// Then:  404 — there is nothing to retag.
func Test_retagForm_when_unknownTag_then_notFound(t *testing.T) {
	h, q := retagTestRouter(t)
	tagTestSeed(t, q)

	rec := tagTestGET(t, h, "/dashboard/repos/test/tags/ghost/retag")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "retag-form") {
		t.Error("no form may render for a missing tag")
	}
}
