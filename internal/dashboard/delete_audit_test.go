package dashboard_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/user/better-docker-registry/internal/auth"
	"github.com/user/better-docker-registry/internal/config"
	"github.com/user/better-docker-registry/internal/dashboard"
	"github.com/user/better-docker-registry/internal/db"
	"github.com/user/better-docker-registry/internal/middleware"
)

// CSRF + audit coverage for the delete flows (plan task 17): DELETE without
// X-CSRF-Token is refused before anything is removed, and every completed
// delete lands in audit_log with an action, the operator's user id and a
// docker-style target reference.

// deleteTestAuthRouter wires login + the session/CSRF-guarded delete tree the
// way the integrating task will: RequireSession injects the session,
// WithSessionCSRF copies its token into middleware.CSRF's context slot.
func deleteTestAuthRouter(t *testing.T) (http.Handler, db.Querier) {
	t.Helper()
	q := repoTestDB(t)
	manager, err := auth.NewSessionManager(q, config.SessionConfig{
		Secret: config.Secret("delete-test-session-secret"),
		TTL:    config.Duration(time.Hour),
	}, false)
	if err != nil {
		t.Fatalf("NewSessionManager: %v", err)
	}

	r := chi.NewRouter()
	r.Post(auth.SessionLoginPath, manager.HandleLogin)
	r.Group(func(r chi.Router) {
		r.Use(manager.RequireSession)
		r.Use(dashboard.WithSessionCSRF)
		r.Use(middleware.CSRF)
		inner := chi.NewRouter()
		dashboard.NewTagsHandler(q).Mount(inner)
		dashboard.NewDeleteHandler(q).Mount(inner)
		r.Mount("/dashboard/repos", dashboard.CollapseRepoName(inner))
	})
	return r, q
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

// Given: the delete routes behind middleware.CSRF with a bound token.
// When:  DELETE arrives without the X-CSRF-Token header.
// Then:  403 on both routes and nothing is deleted.
func Test_delete_when_noCSRFToken_then_403(t *testing.T) {
	h, q := deleteTestRouter(t)
	repo := tagTestSeed(t, q)
	digest := "sha256:" + strings.Repeat("a", 64)

	for _, path := range []string{
		"/dashboard/repos/test/tags/latest",
		"/dashboard/repos/test/manifests/" + digest,
	} {
		rec := deleteTestDELETE(t, h, path, map[string]string{"HX-Request": "true"})
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

// Given: a tag delete through the plain router (no session).
// When:  it completes.
// Then:  audit_log gains one row: action delete_tag, target "test:latest",
// and no user id.
func Test_deleteTag_when_deleted_then_auditRowWritten(t *testing.T) {
	h, q := deleteTestRouter(t)
	tagTestSeed(t, q)

	rec := deleteTestDELETE(t, h, "/dashboard/repos/test/tags/latest", map[string]string{
		middleware.CSRFHeader: gcTestCSRFToken,
		"HX-Request":          "true",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
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

// Given: a signed-in operator on the full session + CSRF stack.
// When:  the manifest is deleted with the token the page rendered.
// Then:  the audit row records the operator's user id and the manifest target.
func Test_delete_when_sessionUser_then_auditRecordsUserID(t *testing.T) {
	h, q := deleteTestAuthRouter(t)
	tagTestSeed(t, q)
	digest := "sha256:" + strings.Repeat("a", 64)

	hash, err := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword: %v", err)
	}
	user, err := q.CreateUser(t.Context(), db.CreateUserParams{
		Username:     "operator",
		PasswordHash: string(hash),
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	cookies := repoTestLogin(t, h)
	token := deleteTestPageToken(t, h, cookies)

	req := httptest.NewRequest(http.MethodDelete, "/dashboard/repos/test/manifests/"+digest, nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set(middleware.CSRFHeader, token)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
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

// deleteTestPageToken fetches the tag list under the session and returns the
// CSRF token the delete button renders into hx-headers.
func deleteTestPageToken(t *testing.T, h http.Handler, cookies []*http.Cookie) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/repos/test", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("tag page status = %d, want 200", rec.Code)
	}
	token := repoTestCSRFToken(t, rec.Body.String())
	if token == "" {
		t.Fatal("tag page rendered no CSRF token into hx-headers")
	}
	return token
}
