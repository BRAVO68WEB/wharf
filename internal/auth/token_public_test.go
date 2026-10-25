package auth_test

import (
	"net/http"
	"testing"

	"github.com/BRAVO68WEB/wharf/internal/auth"
	"github.com/BRAVO68WEB/wharf/internal/db"
)

func Test_TokenEndpoint_when_PublishedPull_then_AnonymousToken(t *testing.T) {
	env := newJWTEnv(t)
	if _, err := env.queries.CreateRepository(t.Context(), "library/nginx"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.queries.SetRepositoryPublished(t.Context(), db.SetRepositoryPublishedParams{
		Published: true, Name: "library/nginx",
	}); err != nil {
		t.Fatal(err)
	}

	rec := env.request(http.MethodGet, "/token?service="+jwtTestService+"&scope=repository:library/nginx:pull", [2]string{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	body := decodeTokenBody(t, rec)
	claims, err := env.signer.Validate(body.Token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != auth.AnonymousSubject {
		t.Errorf("subject = %q", claims.Subject)
	}
	if !claims.HasAccess("library/nginx", "pull") || claims.HasAccess("library/nginx", "push") {
		t.Errorf("access = %+v", claims.Access)
	}
}

func Test_TokenEndpoint_when_UnpublishedOrPush_then_NoAnonymousToken(t *testing.T) {
	env := newJWTEnv(t)
	if _, err := env.queries.CreateRepository(t.Context(), "library/nginx"); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{
		"repository:library/nginx:pull",
		"repository:library/nginx:pull,push",
		"repository:missing:pull",
	} {
		rec := env.request(http.MethodGet, "/token?service="+jwtTestService+"&scope="+scope, [2]string{})
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("scope %s status = %d, want 401", scope, rec.Code)
		}
	}
}
