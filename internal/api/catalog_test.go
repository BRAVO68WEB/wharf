package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"testing"

	"github.com/BRAVO68WEB/wharf/internal/api"
	"github.com/BRAVO68WEB/wharf/internal/db"
)

// catalogBody is the GET /v2/_catalog response:
// {"repositories":["name",...]}. The slice is non-nil so an empty catalog
// marshals as [] rather than null.
type catalogBody struct {
	Repositories []string `json:"repositories"`
}

// decodeCatalog decodes the catalog listing body.
func decodeCatalog(t *testing.T, rec *httptest.ResponseRecorder) catalogBody {
	t.Helper()
	var body catalogBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode catalog body %q: %v", rec.Body.String(), err)
	}
	if body.Repositories == nil {
		t.Fatalf("catalog body %q: repositories is null, want []", rec.Body.String())
	}
	return body
}

// seedRepo registers one repository row and returns it.
func seedRepo(t *testing.T, q db.Querier, name string) db.Repository {
	t.Helper()
	repo, err := q.CreateRepository(t.Context(), name)
	if err != nil {
		t.Fatalf("CreateRepository(%q): %v", name, err)
	}
	return repo
}

// seedRepos registers the named repositories.
func seedRepos(t *testing.T, q db.Querier, names ...string) {
	t.Helper()
	for _, name := range names {
		seedRepo(t, q, name)
	}
}

// seedTags registers a repository holding one manifest and the named tags
// pointing at it (tags carry a foreign key onto a manifest).
func seedTags(t *testing.T, q db.Querier, name string, tags ...string) db.Repository {
	t.Helper()
	repo := seedRepo(t, q, name)
	manifest := digestOf([]byte(name + "-manifest"))
	err := q.UpsertManifest(t.Context(), db.UpsertManifestParams{
		RepoID:    repo.ID,
		Digest:    manifest,
		MediaType: mediaTypeDockerManifest,
		Payload:   []byte("{}"),
		Size:      2,
	})
	if err != nil {
		t.Fatalf("UpsertManifest(%q): %v", name, err)
	}
	for _, tag := range tags {
		err := q.SetTag(t.Context(), db.SetTagParams{
			RepoID:         repo.ID,
			Name:           tag,
			ManifestDigest: manifest,
			UpdatedAt:      1,
		})
		if err != nil {
			t.Fatalf("SetTag(%q, %q): %v", name, tag, err)
		}
	}
	return repo
}

func Test_Catalog_when_Empty_then_ReturnsEmptyRepositories(t *testing.T) {
	// Given: a registry with no repositories
	router, _ := newManifestRouter(t)

	// When: the catalog is listed
	rec := serve(t, router, http.MethodGet, "/v2/_catalog", nil)

	// Then: the body is the empty JSON list, with no next-page link
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	assertHeader(t, rec.Header(), "Content-Type", "application/json")
	assertHeader(t, rec.Header(), "Link", "")
	assertAPIVersion(t, rec)
	if got := rec.Body.String(); got != `{"repositories":[]}` {
		t.Fatalf("body = %s, want {\"repositories\":[]}", got)
	}
}

func Test_Catalog_when_ReposExist_then_ListsSortedNames(t *testing.T) {
	// Given: three repositories in non-alphabetical insertion order
	router, q := newManifestRouter(t)
	seedRepos(t, q, "beta", "alpha", "gamma")

	// When: the catalog is listed
	rec := serve(t, router, http.MethodGet, "/v2/_catalog", nil)

	// Then: every name is returned exactly once, sorted
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	got := decodeCatalog(t, rec).Repositories
	want := []string{"alpha", "beta", "gamma"}
	if !slices.Equal(got, want) {
		t.Fatalf("repositories = %v, want %v", got, want)
	}
}

func Test_Catalog_when_PageSizeGiven_then_PageWithNextLink(t *testing.T) {
	// Given: three repositories
	router, q := newManifestRouter(t)
	seedRepos(t, q, "alpha", "beta", "gamma")

	// When: the first page of two is requested
	rec := serve(t, router, http.MethodGet, "/v2/_catalog?n=2", nil)

	// Then: two names come back and the Link header names the next page
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	got := decodeCatalog(t, rec).Repositories
	if want := []string{"alpha", "beta"}; !slices.Equal(got, want) {
		t.Fatalf("repositories = %v, want %v", got, want)
	}
	assertHeader(t, rec.Header(), "Link", `</v2/_catalog?n=2&last=beta>; rel="next"`)
}

func Test_Catalog_when_LastGiven_then_ContinuesAfterLast(t *testing.T) {
	// Given: three repositories and a first page already read
	router, q := newManifestRouter(t)
	seedRepos(t, q, "alpha", "beta", "gamma")

	// When: the page after "beta" is requested
	rec := serve(t, router, http.MethodGet, "/v2/_catalog?n=2&last=beta", nil)

	// Then: only the remaining name is returned and no next page is advertised
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	got := decodeCatalog(t, rec).Repositories
	if want := []string{"gamma"}; !slices.Equal(got, want) {
		t.Fatalf("repositories = %v, want %v", got, want)
	}
	assertHeader(t, rec.Header(), "Link", "")
}

func Test_Catalog_when_LastAtEnd_then_EmptyPageWithoutLink(t *testing.T) {
	// Given: one repository that the previous page ended on
	router, q := newManifestRouter(t)
	seedRepos(t, q, "alpha")

	// When: the page after that final name is requested
	rec := serve(t, router, http.MethodGet, "/v2/_catalog?n=2&last=alpha", nil)

	// Then: the page is empty and carries no next-page link
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if got := decodeCatalog(t, rec).Repositories; len(got) != 0 {
		t.Fatalf("repositories = %v, want empty", got)
	}
	assertHeader(t, rec.Header(), "Link", "")
}

func Test_Catalog_when_NoPageSize_then_LimitsToDefault100(t *testing.T) {
	// Given: more repositories than the default page holds
	router, q := newManifestRouter(t)
	names := make([]string, 0, 105)
	for i := range 105 {
		names = append(names, "repo-"+strconv.Itoa(1000+i)[1:]) // repo-000 .. repo-104
	}
	seedRepos(t, q, names...)

	// When: the catalog is listed without ?n=
	rec := serve(t, router, http.MethodGet, "/v2/_catalog", nil)

	// Then: exactly 100 names come back and the link continues at repo-099
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	got := decodeCatalog(t, rec).Repositories
	if len(got) != 100 {
		t.Fatalf("len(repositories) = %d, want 100", len(got))
	}
	assertHeader(t, rec.Header(), "Link", `</v2/_catalog?n=100&last=repo-099>; rel="next"`)

	// And: following the link yields the remaining five without another link
	next := serve(t, router, http.MethodGet, "/v2/_catalog?n=100&last=repo-099", nil)
	rest := decodeCatalog(t, next).Repositories
	if len(rest) != 5 {
		t.Fatalf("len(next page) = %d, want 5", len(rest))
	}
	assertHeader(t, next.Header(), "Link", "")
}

func Test_Catalog_when_NestedRepoNames_then_ListsFullPathAndEscapesLink(t *testing.T) {
	// Given: repository names that contain slashes
	router, q := newManifestRouter(t)
	seedRepos(t, q, "library/nginx", "library/redis", "alpha")

	// When: the first two names are requested
	rec := serve(t, router, http.MethodGet, "/v2/_catalog?n=2", nil)

	// Then: the slash survives in the listing and in the escaped Link value
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	got := decodeCatalog(t, rec).Repositories
	if want := []string{"alpha", "library/nginx"}; !slices.Equal(got, want) {
		t.Fatalf("repositories = %v, want %v", got, want)
	}
	assertHeader(t, rec.Header(), "Link", `</v2/_catalog?n=2&last=library%2Fnginx>; rel="next"`)

	// And: following the escaped cursor returns the last name
	next := serve(t, router, http.MethodGet, "/v2/_catalog?n=2&last=library%2Fnginx", nil)
	rest := decodeCatalog(t, next).Repositories
	if want := []string{"library/redis"}; !slices.Equal(rest, want) {
		t.Fatalf("next page = %v, want %v", rest, want)
	}
}

func Test_Catalog_when_DatabaseClosed_then_500InternalError(t *testing.T) {
	// Given: a route tree whose database is closed after wiring
	sqlDB := newTestSQLDB(t)
	router := api.NewRouter(newFakeStorage(), db.New(sqlDB))
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	// When: the catalog is listed
	rec := serve(t, router, http.MethodGet, "/v2/_catalog", nil)

	// Then: the failure is reported as an internal registry error
	requireCode(t, rec, http.StatusInternalServerError, "INTERNAL_ERROR")
}
