package auth_test

import (
	"net/http"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/user/better-docker-registry/internal/auth"
	"github.com/user/better-docker-registry/internal/db"
)

func TestTokenEndpoint_serviceAccountAcceptsToken(t *testing.T) {
	env := newJWTEnv(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("unused-secret-value"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	user, err := env.queries.CreateUser(t.Context(), db.CreateUserParams{
		Username:     "ci",
		PasswordHash: string(hash),
		IsService:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	plain, tokenHash, err := auth.NewAccessToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.queries.CreateAccessToken(t.Context(), db.CreateAccessTokenParams{
		UserID: user.ID, Name: "default", TokenHash: tokenHash,
	}); err != nil {
		t.Fatal(err)
	}

	ok := env.request(http.MethodGet, "/token?service="+jwtTestService+"&scope=repository:app:pull", [2]string{"ci", plain})
	if ok.Code != http.StatusOK {
		t.Fatalf("token login = %d body=%s", ok.Code, ok.Body.String())
	}
	denied := env.request(http.MethodGet, "/token?service="+jwtTestService+"&scope=repository:app:pull", [2]string{"ci", "unused-secret-value"})
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("service password login = %d, want 401", denied.Code)
	}
	if err := env.queries.RevokeAccessTokensByUser(t.Context(), user.ID); err != nil {
		t.Fatal(err)
	}
	revoked := env.request(http.MethodGet, "/token?service="+jwtTestService+"&scope=repository:app:pull", [2]string{"ci", plain})
	if revoked.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token login = %d, want 401", revoked.Code)
	}
}

func TestTokenEndpoint_humanTokenRevokeKeepsPassword(t *testing.T) {
	env := newJWTEnv(t)
	const password = "human-password-ok"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	user, err := env.queries.CreateUser(t.Context(), db.CreateUserParams{
		Username:     "ada",
		PasswordHash: string(hash),
	})
	if err != nil {
		t.Fatal(err)
	}
	plain, tokenHash, err := auth.NewAccessToken()
	if err != nil {
		t.Fatal(err)
	}
	row, err := env.queries.CreateAccessToken(t.Context(), db.CreateAccessTokenParams{
		UserID: user.ID, Name: "laptop", TokenHash: tokenHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/token?service=" + jwtTestService + "&scope=repository:app:pull"
	if code := env.request(http.MethodGet, path, [2]string{"ada", plain}).Code; code != http.StatusOK {
		t.Fatalf("token login = %d", code)
	}
	if code := env.request(http.MethodGet, path, [2]string{"ada", password}).Code; code != http.StatusOK {
		t.Fatalf("password login = %d", code)
	}
	n, err := env.queries.RevokeAccessToken(t.Context(), db.RevokeAccessTokenParams{ID: row.ID, UserID: user.ID})
	if err != nil || n != 1 {
		t.Fatalf("revoke = %d, %v", n, err)
	}
	if code := env.request(http.MethodGet, path, [2]string{"ada", plain}).Code; code != http.StatusUnauthorized {
		t.Fatalf("revoked token login = %d, want 401", code)
	}
	if code := env.request(http.MethodGet, path, [2]string{"ada", password}).Code; code != http.StatusOK {
		t.Fatalf("password after revoke = %d, want 200", code)
	}
}
