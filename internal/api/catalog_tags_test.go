package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/BRAVO68WEB/wharf/internal/api"
	"github.com/BRAVO68WEB/wharf/internal/db"
)

// tagsBody is the GET /v2/{name}/tags/list response:
// {"name":"name","tags":["tag",...]}. The slice is non-nil so a repository
// without tags marshals as [] rather than null.
type tagsBody struct {
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

// decodeTags decodes the tag listing body.
func decodeTags(t *testing.T, rec *httptest.ResponseRecorder) tagsBody {
	t.Helper()
	var body tagsBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode tags body %q: %v", rec.Body.String(), err)
	}
	if body.Tags == nil {
		t.Fatalf("tags body %q: tags is null, want []", rec.Body.String())
	}
	return body
}

func Test_TagsList_when_RepoExists_then_ListsSortedTags(t *testing.T) {
	// Given: a repository with tags in non-alphabetical insertion order
	router, q := newManifestRouter(t)
	seedTags(t, q, "test", "v2", "latest", "v1")

	// When: its tags are listed
	rec := serve(t, router, http.MethodGet, "/v2/test/tags/list", nil)

	// Then: every tag comes back exactly once, sorted, under the repo name
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	assertAPIVersion(t, rec)
	body := decodeTags(t, rec)
	if body.Name != "test" {
		t.Fatalf("name = %q, want %q", body.Name, "test")
	}
	if want := []string{"latest", "v1", "v2"}; !slices.Equal(body.Tags, want) {
		t.Fatalf("tags = %v, want %v", body.Tags, want)
	}
}

func Test_TagsList_when_PageSizeGiven_then_PageWithNextLink(t *testing.T) {
	// Given: a repository with three tags
	router, q := newManifestRouter(t)
	seedTags(t, q, "test", "v1", "v2", "v3")

	// When: the first page of two tags is requested
	rec := serve(t, router, http.MethodGet, "/v2/test/tags/list?n=2", nil)

	// Then: two tags come back and the Link header names the next page
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	got := decodeTags(t, rec).Tags
	if want := []string{"v1", "v2"}; !slices.Equal(got, want) {
		t.Fatalf("tags = %v, want %v", got, want)
	}
	assertHeader(t, rec.Header(), "Link", `</v2/test/tags/list?n=2&last=v2>; rel="next"`)

	// And: following the link yields the last tag without another link
	next := serve(t, router, http.MethodGet, "/v2/test/tags/list?n=2&last=v2", nil)
	rest := decodeTags(t, next).Tags
	if want := []string{"v3"}; !slices.Equal(rest, want) {
		t.Fatalf("next page = %v, want %v", rest, want)
	}
	assertHeader(t, next.Header(), "Link", "")
}

func Test_TagsList_when_RepoMissing_then_404NameInvalid(t *testing.T) {
	// Given: a registry that holds no repositories
	router, _ := newManifestRouter(t)

	// When: the tags of an unknown repository are listed
	rec := serve(t, router, http.MethodGet, "/v2/nope/tags/list", nil)

	// Then: the registry answers the pinned 404 NAME_INVALID contract
	requireCode(t, rec, http.StatusNotFound, "NAME_INVALID")
	want := `{"errors":[{"code":"NAME_INVALID","message":"repository name not known to the registry","detail":{}}]}`
	if got := rec.Body.String(); got != want {
		t.Fatalf("body = %s, want %s", got, want)
	}
}

func Test_TagsList_when_RepoHasNoTags_then_ReturnsEmptyTags(t *testing.T) {
	// Given: a repository pushed by digest only (no tags)
	router, q := newManifestRouter(t)
	seedRepo(t, q, "test")

	// When: its tags are listed
	rec := serve(t, router, http.MethodGet, "/v2/test/tags/list", nil)

	// Then: the empty list is returned under the repository name
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	body := decodeTags(t, rec)
	if body.Name != "test" {
		t.Fatalf("name = %q, want %q", body.Name, "test")
	}
	if len(body.Tags) != 0 {
		t.Fatalf("tags = %v, want empty", body.Tags)
	}
}

func Test_TagsList_when_NestedRepoName_then_ListsTags(t *testing.T) {
	// Given: a repository whose name contains slashes
	router, q := newManifestRouter(t)
	seedTags(t, q, "library/nginx", "stable")

	// When: its tags are listed through the collapsed route
	rec := serve(t, router, http.MethodGet, "/v2/library/nginx/tags/list", nil)

	// Then: the slash name resolves and its tag is listed
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	body := decodeTags(t, rec)
	if body.Name != "library/nginx" {
		t.Fatalf("name = %q, want %q", body.Name, "library/nginx")
	}
	if want := []string{"stable"}; !slices.Equal(body.Tags, want) {
		t.Fatalf("tags = %v, want %v", body.Tags, want)
	}
}

func Test_TagsList_when_UnparsablePageSize_then_UsesDefaultPage(t *testing.T) {
	// Given: a repository with three tags
	router, q := newManifestRouter(t)
	seedTags(t, q, "test", "v1", "v2", "v3")

	// When: a page size that is not a number is requested
	rec := serve(t, router, http.MethodGet, "/v2/test/tags/list?n=abc", nil)

	// Then: the request falls back to the default page and still lists bounded
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	got := decodeTags(t, rec).Tags
	if want := []string{"v1", "v2", "v3"}; !slices.Equal(got, want) {
		t.Fatalf("tags = %v, want %v", got, want)
	}
	assertHeader(t, rec.Header(), "Link", "")
}

func Test_TagsList_when_DatabaseClosed_then_500InternalError(t *testing.T) {
	// Given: a route tree whose database is closed after wiring
	sqlDB := newTestSQLDB(t)
	router := api.NewRouter(newFakeStorage(), db.New(sqlDB))
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	// When: a tag listing is requested
	rec := serve(t, router, http.MethodGet, "/v2/test/tags/list", nil)

	// Then: the failure is reported as an internal registry error
	requireCode(t, rec, http.StatusInternalServerError, "INTERNAL_ERROR")
}
