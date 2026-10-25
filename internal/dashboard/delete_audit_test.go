package dashboard_test

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/user/better-docker-registry/internal/auth"
	"github.com/user/better-docker-registry/internal/dashboard"
	"github.com/user/better-docker-registry/internal/db"
	"github.com/user/better-docker-registry/internal/gc"
	"github.com/user/better-docker-registry/internal/middleware"
)

// CSRF and audit coverage for the JSON delete routes: DELETE without
// X-CSRF-Token is refused before anything is removed, and every completed
// delete lands in audit_log with an action, the operator's user id, and a
// docker-style target reference.

func apiCSRFRouter(t *testing.T) (http.Handler, db.Querier) {
	t.Helper()
	sqlDB, err := db.OpenAndMigrate(t.Context(), filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	q := db.New(sqlDB)
	inner := chi.NewRouter()
	dashboard.NewHTTPAPI(sqlDB, q, newGCTestStore(), gc.NewLock()).Mount(inner)
	return gcTestWithCSRF(inner), q
}

// deleteTestAuditRows returns the audit log, newest first.
func deleteTestAuditRows(t *testing.T, q db.Querier) []db.AuditLog {
	t.Helper()
	rows, err := q.ListAuditEntriesFromStart(t.Context(), 10)
	if err != nil {
		t.Fatalf("ListAuditEntriesFromStart: %v", err)
	}
	return rows
}

// seedTwoTags creates repository "test" with one manifest and tags latest
// and v1.0.
func seedTwoTags(t *testing.T, q db.Querier) db.Repository {
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

// Given: the delete routes behind middleware.CSRF with a bound token.
// When:  DELETE arrives without the X-CSRF-Token header.
// Then:  403 on both routes and nothing is deleted.
func Test_delete_when_noCSRFToken_then_403(t *testing.T) {
	h, q := apiCSRFRouter(t)
	repo := seedTwoTags(t, q)
	digest := "sha256:" + strings.Repeat("a", 64)

	for _, path := range []string{
		"/api/repo/test/tags/latest",
		"/api/repo/test/manifests/" + digest,
	} {
		rec := apiDo(t, h, http.MethodDelete, path, "")
		if rec.Code != http.StatusForbidden {
			t.Errorf("DELETE %s without token: status = %d, want 403", path, rec.Code)
		}
	}

	ctx := t.Context()
	if _, err := q.GetTag(ctx, db.GetTagParams{RepoID: repo.ID, Name: "latest"}); err != nil {
		t.Errorf("tag latest must survive CSRF refusals: %v", err)
	}
	if _, err := q.GetManifest(ctx, db.GetManifestParams{RepoID: repo.ID, Digest: digest}); err != nil {
		t.Errorf("manifest must survive CSRF refusals: %v", err)
	}
}

// Given: the same routes, and a request that carries the bound token.
// When:  DELETE runs.
// Then:  the tag is removed. CSRF allows the call.
func Test_delete_when_CSRFToken_then_tagRemoved(t *testing.T) {
	h, q := apiCSRFRouter(t)
	repo := seedTwoTags(t, q)

	req := httptest.NewRequest(http.MethodDelete, "/api/repo/test/tags/latest", nil)
	req.Header.Set(middleware.CSRFHeader, gcTestCSRFToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body)
	}
	if _, err := q.GetTag(t.Context(), db.GetTagParams{RepoID: repo.ID, Name: "latest"}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetTag(latest) err = %v, want sql.ErrNoRows", err)
	}
}

// Given: a tag delete with no signed-in user.
// When:  it completes.
// Then:  audit_log gains one row: action delete_tag, target "test:latest",
// and no user id.
func Test_deleteTag_when_deleted_then_auditRowWritten(t *testing.T) {
	h, q, _ := apiTestRouter(t)
	seedTwoTags(t, q)

	rec := apiDo(t, h, http.MethodDelete, "/api/repo/test/tags/latest", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body)
	}

	rows := deleteTestAuditRows(t, q)
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.Action != "delete_tag" {
		t.Errorf("action = %q, want delete_tag", row.Action)
	}
	if row.Target != "test:latest" {
		t.Errorf("target = %q, want test:latest", row.Target)
	}
	if row.UserID.Valid {
		t.Errorf("user id = %d, want NULL for a session-less delete", row.UserID.Int64)
	}
	if row.CreatedAt <= 0 {
		t.Errorf("created_at = %d, want a unix timestamp", row.CreatedAt)
	}
}

// Given: a signed-in operator.
// When:  the manifest is deleted.
// Then:  the audit row records the operator's user id and the manifest target.
func Test_delete_when_sessionUser_then_auditRecordsUserID(t *testing.T) {
	h, q, _ := apiTestRouter(t)
	seedTwoTags(t, q)
	digest := "sha256:" + strings.Repeat("a", 64)
	user, err := q.CreateUser(t.Context(), db.CreateUserParams{
		Username:     "operator",
		PasswordHash: "x",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	rec := apiDoAs(t, h, auth.SessionUser{ID: user.ID, Username: "operator"},
		http.MethodDelete, "/api/repo/test/manifests/"+digest, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body)
	}

	rows := deleteTestAuditRows(t, q)
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.Action != "delete_manifest" {
		t.Errorf("action = %q, want delete_manifest", row.Action)
	}
	want := "test@" + digest
	if row.Target != want {
		t.Errorf("target = %q, want %q", row.Target, want)
	}
	if !row.UserID.Valid || row.UserID.Int64 != user.ID {
		t.Errorf("user id = %+v, want %d", row.UserID, user.ID)
	}
}

// Given: repo "test" with tags "latest" and "v1.0" on one manifest.
// When:  DELETE latest.
// Then:  that tag is gone, and the manifest plus the sibling tag survive.
func Test_deleteTag_when_oneOfTwo_then_onlyThatTagRemoved(t *testing.T) {
	h, q, _ := apiTestRouter(t)
	repo := seedTwoTags(t, q)

	rec := apiDo(t, h, http.MethodDelete, "/api/repo/test/tags/latest", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body)
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

// Given: a manifest with the single tag "latest".
// When:  that tag is deleted.
// Then:  the manifest row survives. A tag delete unmaps a name only.
func Test_deleteTag_when_lastTag_then_manifestSurvives(t *testing.T) {
	h, q, _ := apiTestRouter(t)
	digest := manifestTestSeed(t, q, "test", manifestTestImagePayload,
		"application/vnd.docker.distribution.manifest.v2+json")

	rec := apiDo(t, h, http.MethodDelete, "/api/repo/test/tags/latest", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body)
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

// Given: requests naming a missing repo or tag.
// When:  DELETE runs.
// Then:  404 for both, and nothing is deleted.
func Test_deleteTag_when_unknown_then_notFound(t *testing.T) {
	h, q, _ := apiTestRouter(t)
	repo := seedTwoTags(t, q)

	for _, path := range []string{
		"/api/repo/missing/nope/tags/latest",
		"/api/repo/test/tags/ghost",
	} {
		rec := apiDo(t, h, http.MethodDelete, path, "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("DELETE %s: status = %d, want 404", path, rec.Code)
		}
	}

	if _, err := q.GetTag(t.Context(), db.GetTagParams{RepoID: repo.ID, Name: "latest"}); err != nil {
		t.Errorf("tag latest must survive the unknown-target deletes: %v", err)
	}
}

// Given: the multi-segment repo "library/nginx" with a tag.
// When:  DELETE /api/repo/library/nginx/tags/latest.
// Then:  the collapsed name routes to the handler and the tag is gone.
func Test_deleteTag_when_multisegmentRepo_then_routed(t *testing.T) {
	h, q, _ := apiTestRouter(t)
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

	rec := apiDo(t, h, http.MethodDelete, "/api/repo/library/nginx/tags/latest", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body)
	}
	if _, err := q.GetTag(ctx, db.GetTagParams{RepoID: repo.ID, Name: "latest"}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetTag err = %v, want sql.ErrNoRows", err)
	}
}

// Given: one manifest carrying tags "latest" and "v1.0".
// When:  DELETE the manifest by digest.
// Then:  204, the manifest is gone, and so is every tag on it.
func Test_deleteManifest_when_deleted_then_manifestAndAllTagsRemoved(t *testing.T) {
	h, q, _ := apiTestRouter(t)
	repo := seedTwoTags(t, q)
	digest := "sha256:" + strings.Repeat("a", 64)

	rec := apiDo(t, h, http.MethodDelete, "/api/repo/test/manifests/"+digest, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body)
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

// Given: a repo with one manifest.
// When:  DELETE names an unknown digest or an unknown repo.
// Then:  404 and the existing rows are untouched.
func Test_deleteManifest_when_unknown_then_notFound(t *testing.T) {
	h, q, _ := apiTestRouter(t)
	repo := seedTwoTags(t, q)

	for _, path := range []string{
		"/api/repo/test/manifests/sha256:" + strings.Repeat("f", 64),
		"/api/repo/missing/manifests/sha256:" + strings.Repeat("a", 64),
	} {
		rec := apiDo(t, h, http.MethodDelete, path, "")
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
