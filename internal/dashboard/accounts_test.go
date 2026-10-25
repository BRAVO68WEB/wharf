package dashboard_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/user/better-docker-registry/internal/auth"
	"github.com/user/better-docker-registry/internal/dashboard"
	"github.com/user/better-docker-registry/internal/db"
	"github.com/user/better-docker-registry/internal/gc"
)

func TestAPI_accounts_serviceTokenAndAdminGate(t *testing.T) {
	h, q, _ := apiTestRouter(t)
	admin, err := q.CreateUser(t.Context(), db.CreateUserParams{
		Username: "admin", PasswordHash: "x", IsAdmin: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	denied := apiDo(t, h, http.MethodPost, "/api/accounts", `{"username":"ci","service":true}`)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("anonymous create = %d", denied.Code)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/accounts", strings.NewReader(`{"username":"ci","service":true}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.ContextWithSession(req.Context(), auth.SessionUser{ID: admin.ID, Username: "admin", IsAdmin: true}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"token":"bdr_`) {
		t.Fatalf("create service = %d body=%s", rec.Code, rec.Body.String())
	}

	short := httptest.NewRequest(http.MethodPost, "/api/accounts", strings.NewReader(`{"username":"ada","password":"short"}`))
	short.Header.Set("Content-Type", "application/json")
	short = short.WithContext(auth.ContextWithSession(short.Context(), auth.SessionUser{ID: admin.ID, Username: "admin", IsAdmin: true}))
	bad := httptest.NewRecorder()
	h.ServeHTTP(bad, short)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("short password = %d", bad.Code)
	}
}

func TestAPI_accounts_rotationIsAtomicAndConflictsAreHonest(t *testing.T) {
	sqlDB, err := db.OpenAndMigrate(t.Context(), filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	q := db.New(sqlDB)
	r := chi.NewRouter()
	dashboard.NewHTTPAPI(sqlDB, q, newGCTestStore(), gc.NewLock()).Mount(r)
	admin, err := q.CreateUser(t.Context(), db.CreateUserParams{Username: "admin", PasswordHash: "x", IsAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	adminUser := auth.SessionUser{ID: admin.ID, Username: "admin", IsAdmin: true}

	created := apiDoAs(t, r, adminUser, http.MethodPost, "/api/accounts", `{"username":"ci","service":true}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d body=%s", created.Code, created.Body.String())
	}
	var body struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !tokenStillActive(t, q, body.ID) {
		t.Fatal("new service account has no active token")
	}

	bad := apiDoAs(t, r, adminUser, http.MethodPost, "/api/accounts/"+strconv.FormatInt(body.ID, 10)+"/tokens", "not-json")
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("bad json = %d body=%s", bad.Code, bad.Body.String())
	}
	if !tokenStillActive(t, q, body.ID) {
		t.Fatal("bad json revoked the service token")
	}

	if _, err := sqlDB.Exec(`CREATE TRIGGER fail_token_insert BEFORE INSERT ON access_tokens
BEGIN
  SELECT RAISE(ROLLBACK, 'boom');
END`); err != nil {
		t.Fatal(err)
	}
	failed := apiDoAs(t, r, adminUser, http.MethodPost, "/api/accounts/"+strconv.FormatInt(body.ID, 10)+"/tokens", `{"name":"default"}`)
	if failed.Code != http.StatusInternalServerError {
		t.Fatalf("failed rotate = %d body=%s", failed.Code, failed.Body.String())
	}
	if !tokenStillActive(t, q, body.ID) {
		t.Fatal("failed rotate revoked the service token")
	}
	if _, err := sqlDB.Exec(`DROP TRIGGER fail_token_insert`); err != nil {
		t.Fatal(err)
	}

	again := apiDoAs(t, r, adminUser, http.MethodPost, "/api/accounts", `{"username":"ci","service":true}`)
	if again.Code != http.StatusConflict {
		t.Fatalf("duplicate = %d body=%s", again.Code, again.Body.String())
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	down := apiDoAs(t, r, adminUser, http.MethodPost, "/api/accounts", `{"username":"other","password":"long-enough"}`)
	if down.Code != http.StatusInternalServerError {
		t.Fatalf("closed database = %d body=%s", down.Code, down.Body.String())
	}
}

func tokenStillActive(t *testing.T, q db.Querier, userID int64) bool {
	t.Helper()
	rows, err := q.ListAccessTokensByUser(t.Context(), userID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if !row.RevokedAt.Valid {
			return true
		}
	}
	return false
}
