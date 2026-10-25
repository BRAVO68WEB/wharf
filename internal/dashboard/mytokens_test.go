package dashboard_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/BRAVO68WEB/wharf/internal/auth"
	"github.com/BRAVO68WEB/wharf/internal/db"
)

func TestAPI_myTokens_createListRevoke(t *testing.T) {
	h, q, _ := apiTestRouter(t)
	ada, err := q.CreateUser(t.Context(), db.CreateUserParams{Username: "ada", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	grace, err := q.CreateUser(t.Context(), db.CreateUserParams{Username: "grace", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	adaUser := auth.SessionUser{ID: ada.ID, Username: "ada"}
	graceUser := auth.SessionUser{ID: grace.ID, Username: "grace"}

	anon := apiDo(t, h, http.MethodPost, "/api/tokens", `{"name":"laptop"}`)
	if anon.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous create = %d", anon.Code)
	}
	blank := apiDoAs(t, h, adaUser, http.MethodPost, "/api/tokens", `{"name":"  "}`)
	if blank.Code != http.StatusBadRequest {
		t.Fatalf("blank name = %d body=%s", blank.Code, blank.Body.String())
	}
	service := apiDoAs(t, h, auth.SessionUser{ID: ada.ID, Username: "ada", IsService: true}, http.MethodGet, "/api/tokens", "")
	if service.Code != http.StatusUnauthorized {
		t.Fatalf("service list = %d", service.Code)
	}

	created := apiDoAs(t, h, adaUser, http.MethodPost, "/api/tokens", `{"name":"laptop"}`)
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"token":"wharf_`) {
		t.Fatalf("create = %d body=%s", created.Code, created.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(created.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	token, _ := body["token"].(string)
	if !strings.HasPrefix(token, "wharf_") || strings.Contains(created.Body.String(), "token_hash") {
		t.Fatalf("create body = %s", created.Body.String())
	}

	listed := apiDoAs(t, h, adaUser, http.MethodGet, "/api/tokens", "")
	if listed.Code != http.StatusOK {
		t.Fatalf("list = %d body=%s", listed.Code, listed.Body.String())
	}
	var list struct {
		Tokens []struct {
			ID   float64 `json:"id"`
			Name string  `json:"name"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Tokens) != 1 || list.Tokens[0].Name != "laptop" {
		t.Fatalf("tokens = %#v", list.Tokens)
	}
	if strings.Contains(listed.Body.String(), "wharf_") {
		t.Fatal("list returned a token secret")
	}
	id := int64(list.Tokens[0].ID)

	stolen := apiDoAs(t, h, graceUser, http.MethodDelete, "/api/tokens/"+strconv.FormatInt(id, 10), "")
	if stolen.Code != http.StatusNotFound {
		t.Fatalf("other user revoke = %d body=%s", stolen.Code, stolen.Body.String())
	}
	still := apiDoAs(t, h, adaUser, http.MethodGet, "/api/tokens", "")
	if !strings.Contains(still.Body.String(), `"laptop"`) {
		t.Fatalf("token disappeared after a foreign revoke: %s", still.Body.String())
	}

	gone := apiDoAs(t, h, adaUser, http.MethodDelete, "/api/tokens/"+strconv.FormatInt(id, 10), "")
	if gone.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d body=%s", gone.Code, gone.Body.String())
	}
	after := apiDoAs(t, h, adaUser, http.MethodGet, "/api/tokens", "")
	if strings.Contains(after.Body.String(), `"laptop"`) {
		t.Fatalf("revoked token still listed: %s", after.Body.String())
	}
	again := apiDoAs(t, h, adaUser, http.MethodDelete, "/api/tokens/"+strconv.FormatInt(id, 10), "")
	if again.Code != http.StatusNotFound {
		t.Fatalf("second revoke = %d", again.Code)
	}
}

func apiDoAs(t *testing.T, h http.Handler, user auth.SessionUser, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req = req.WithContext(auth.ContextWithSession(req.Context(), user))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
