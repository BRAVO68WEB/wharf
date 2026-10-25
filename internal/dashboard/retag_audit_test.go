package dashboard_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/user/better-docker-registry/internal/db"
)

// Audit and input-validation coverage for the JSON retag route: a POST
// without X-CSRF-Token is refused before anything is written, every
// completed retag lands in audit_log, and malformed input is refused before
// anything is copied.

// Given: the retag route behind middleware.CSRF with a bound token.
// When:  POST arrives without the X-CSRF-Token header.
// Then:  403 and no tag is created.
func Test_retag_when_noCSRFToken_then_403(t *testing.T) {
	h, q := apiCSRFRouter(t)
	repo := seedTwoTags(t, q)

	rec := apiDo(t, h, http.MethodPost, "/api/repo/test/tags/latest/retag", `{"targetTag":"v2.0"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body)
	}
	if _, err := q.GetTag(t.Context(), db.GetTagParams{RepoID: repo.ID, Name: "v2.0"}); err == nil {
		t.Error("tag v2.0 must not exist after a CSRF refusal")
	}
}

// Given: a completed retag with no signed-in user.
// When:  it lands.
// Then:  audit_log gains one row: action retag_tag, target
// "test:latest->test:v2.0", and no user id.
func Test_retag_when_completed_then_auditRowWritten(t *testing.T) {
	h, q, _ := apiTestRouter(t)
	manifestTestSeed(t, q, "test", manifestTestImagePayload,
		"application/vnd.docker.distribution.manifest.v2+json")

	rec := retagAPI(t, h, "test", "latest", "test", "v2.0")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
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

// Given: a new tag name outside the OCI tag grammar.
// When:  the retag is submitted.
// Then:  400 and nothing is copied.
func Test_retag_when_invalidNewTag_then_400(t *testing.T) {
	h, q, _ := apiTestRouter(t)
	repo := seedTwoTags(t, q)

	rec := retagAPI(t, h, "test", "latest", "test", "bad!tag")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "new tag must match") {
		t.Errorf("body = %s, want the tag grammar", rec.Body.String())
	}
	if _, err := q.GetTag(t.Context(), db.GetTagParams{RepoID: repo.ID, Name: "bad!tag"}); err == nil {
		t.Error("invalid tag must not be created")
	}
}

// Given: a target repository outside the OCI name grammar.
// When:  the retag is submitted.
// Then:  400 and nothing is copied.
func Test_retag_when_invalidTargetRepo_then_400(t *testing.T) {
	h, q, _ := apiTestRouter(t)
	repo := seedTwoTags(t, q)

	rec := retagAPI(t, h, "test", "latest", "Bad/Name", "v2.0")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body)
	}
	if _, err := q.GetTag(t.Context(), db.GetTagParams{RepoID: repo.ID, Name: "v2.0"}); err == nil {
		t.Error("tag v2.0 must not be created for an invalid target repo")
	}
}

// Given: a retag naming a missing repo or a missing source tag.
// When:  the retag is submitted.
// Then:  404, no audit row, and nothing is created.
func Test_retag_when_unknownSource_then_notFound(t *testing.T) {
	h, q, _ := apiTestRouter(t)
	seedTwoTags(t, q)

	for _, path := range []string{
		"/api/repo/missing/nope/tags/latest/retag",
		"/api/repo/test/tags/ghost/retag",
	} {
		rec := apiDo(t, h, http.MethodPost, path, `{"targetTag":"v2.0"}`)
		if rec.Code != http.StatusNotFound {
			t.Errorf("POST %s: status = %d, want 404; body=%s", path, rec.Code, rec.Body)
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
