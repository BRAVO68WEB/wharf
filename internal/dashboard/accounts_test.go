package dashboard_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/user/better-docker-registry/internal/auth"
	"github.com/user/better-docker-registry/internal/db"
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
