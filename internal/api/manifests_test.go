package api_test

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/user/better-docker-registry/internal/db"
)

func Test_GetManifest_when_PushedByTag_then_ReturnsIdenticalBytes(t *testing.T) {
	// Given: a manifest pushed under a tag
	router, _ := newManifestRouter(t)
	payload := imageManifestJSON(t, mediaTypeDockerManifest, digestOf([]byte("config")), digestOf([]byte("layer")))
	digest := digestOf(payload)
	rec := putManifest(t, router, "/v2/test/manifests/latest", payload, mediaTypeDockerManifest)
	if rec.Code != http.StatusCreated {
		t.Fatalf("push status = %d, want %d (body %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	// When: the manifest is fetched by tag
	got := serve(t, router, http.MethodGet, "/v2/test/manifests/latest", map[string]string{
		"Accept": mediaTypeDockerManifest,
	})

	// Then: the identical bytes come back with the registry headers
	if got.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", got.Code, http.StatusOK, got.Body.String())
	}
	if !bytes.Equal(got.Body.Bytes(), payload) {
		t.Fatalf("body = %q, want %q", got.Body.String(), payload)
	}
	assertManifestHeaders(t, got, digest, mediaTypeDockerManifest, len(payload))
}

func Test_GetManifest_when_PushedByDigest_then_ReturnsIdenticalBytes(t *testing.T) {
	// Given: a manifest pushed under its own digest
	router, _ := newManifestRouter(t)
	payload := imageManifestJSON(t, mediaTypeDockerManifest, digestOf([]byte("cfg")))
	digest := digestOf(payload)
	if rec := putManifest(t, router, "/v2/test/manifests/"+digest, payload, mediaTypeDockerManifest); rec.Code != http.StatusCreated {
		t.Fatalf("push status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}

	// When: the manifest is fetched by digest
	got := serve(t, router, http.MethodGet, "/v2/test/manifests/"+digest, nil)

	// Then: the identical bytes come back
	if got.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", got.Code, http.StatusOK, got.Body.String())
	}
	if !bytes.Equal(got.Body.Bytes(), payload) {
		t.Fatalf("body = %q, want %q", got.Body.String(), payload)
	}
	assertManifestHeaders(t, got, digest, mediaTypeDockerManifest, len(payload))
}

func Test_GetManifest_when_Index_then_ReturnsStoredIndex(t *testing.T) {
	// Given: a multi-arch index pushed under a tag
	router, _ := newManifestRouter(t)
	payload := indexManifestJSON(t, mediaTypeOCIIndex,
		digestOf([]byte("amd64")), digestOf([]byte("arm64")))
	if rec := putManifest(t, router, "/v2/test/manifests/multi", payload, mediaTypeOCIIndex); rec.Code != http.StatusCreated {
		t.Fatalf("push status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}

	// When: the index is fetched
	got := serve(t, router, http.MethodGet, "/v2/test/manifests/multi", nil)

	// Then: the index comes back under its OCI media type
	if got.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", got.Code, http.StatusOK, got.Body.String())
	}
	if !bytes.Equal(got.Body.Bytes(), payload) {
		t.Fatalf("body = %q, want %q", got.Body.String(), payload)
	}
	assertManifestHeaders(t, got, digestOf(payload), mediaTypeOCIIndex, len(payload))
}

func Test_HeadManifest_when_Exists_then_ReturnsHeadersWithoutBody(t *testing.T) {
	// Given: a pushed manifest
	router, _ := newManifestRouter(t)
	payload := imageManifestJSON(t, mediaTypeDockerManifest, digestOf([]byte("cfg")))
	if rec := putManifest(t, router, "/v2/test/manifests/latest", payload, mediaTypeDockerManifest); rec.Code != http.StatusCreated {
		t.Fatalf("push status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}

	// When: HEAD fetches it
	got := serve(t, router, http.MethodHead, "/v2/test/manifests/latest", nil)

	// Then: the headers describe the manifest and no body is written
	if got.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", got.Code, http.StatusOK, got.Body.String())
	}
	assertManifestHeaders(t, got, digestOf(payload), mediaTypeDockerManifest, len(payload))
	if got.Body.Len() != 0 {
		t.Fatalf("HEAD body = %d bytes, want 0", got.Body.Len())
	}
}

func Test_GetManifest_when_TagMoves_then_ReturnsNewManifest(t *testing.T) {
	// Given: one tag overwritten by a second push
	router, _ := newManifestRouter(t)
	first := imageManifestJSON(t, mediaTypeDockerManifest, digestOf([]byte("one")))
	second := imageManifestJSON(t, mediaTypeDockerManifest, digestOf([]byte("two")))
	if rec := putManifest(t, router, "/v2/test/manifests/latest", first, mediaTypeDockerManifest); rec.Code != http.StatusCreated {
		t.Fatalf("first push status = %d, want 201", rec.Code)
	}
	if rec := putManifest(t, router, "/v2/test/manifests/latest", second, mediaTypeDockerManifest); rec.Code != http.StatusCreated {
		t.Fatalf("second push status = %d, want 201", rec.Code)
	}

	// When: the tag is fetched
	got := serve(t, router, http.MethodGet, "/v2/test/manifests/latest", nil)

	// Then: it resolves to the second manifest while the first stays by digest
	if !bytes.Equal(got.Body.Bytes(), second) {
		t.Fatalf("tag body = %q, want the second manifest", got.Body.String())
	}
	old := serve(t, router, http.MethodGet, "/v2/test/manifests/"+digestOf(first), nil)
	if old.Code != http.StatusOK {
		t.Fatalf("old digest status = %d, want %d", old.Code, http.StatusOK)
	}
}

func Test_GetManifest_when_NestedRepoName_then_ReturnsManifest(t *testing.T) {
	// Given: a manifest pushed to a repository whose name contains a slash
	router, _ := newManifestRouter(t)
	payload := imageManifestJSON(t, mediaTypeDockerManifest, digestOf([]byte("cfg")))
	if rec := putManifest(t, router, "/v2/library/nginx/manifests/latest", payload, mediaTypeDockerManifest); rec.Code != http.StatusCreated {
		t.Fatalf("push status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}

	// When: it is fetched back under the nested name
	got := serve(t, router, http.MethodGet, "/v2/library/nginx/manifests/latest", nil)

	// Then: the manifest is served like any other
	if got.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", got.Code, http.StatusOK, got.Body.String())
	}
	if !bytes.Equal(got.Body.Bytes(), payload) {
		t.Fatalf("body = %q, want %q", got.Body.String(), payload)
	}
}

func Test_GetManifest_when_SameDigestTwoRepos_then_EachRepoHoldsIt(t *testing.T) {
	// Given: the same manifest pushed to two repositories
	router, q := newManifestRouter(t)
	payload := imageManifestJSON(t, mediaTypeDockerManifest, digestOf([]byte("cfg")))
	digest := digestOf(payload)
	for _, path := range []string{"/v2/alpha/manifests/v1", "/v2/beta/manifests/v1"} {
		if rec := putManifest(t, router, path, payload, mediaTypeDockerManifest); rec.Code != http.StatusCreated {
			t.Fatalf("push %s status = %d, want 201", path, rec.Code)
		}
	}

	// When: each repository is asked for the digest
	for _, name := range []string{"alpha", "beta"} {
		got := serve(t, router, http.MethodGet, "/v2/"+name+"/manifests/"+digest, nil)
		if got.Code != http.StatusOK {
			t.Fatalf("get %s status = %d, want %d", name, got.Code, http.StatusOK)
		}
	}

	// Then: both repositories have their own manifest row
	for _, name := range []string{"alpha", "beta"} {
		repo, err := q.GetRepositoryByName(t.Context(), name)
		if err != nil {
			t.Fatalf("repo %s: %v", name, err)
		}
		m, err := q.GetManifest(t.Context(), db.GetManifestParams{RepoID: repo.ID, Digest: digest})
		if err != nil {
			t.Fatalf("manifest in %s: %v", name, err)
		}
		if !bytes.Equal(m.Payload, payload) {
			t.Fatalf("manifest in %s has wrong payload", name)
		}
	}
}

func Test_GetManifest_when_UnknownManifest_then_ManifestUnknown(t *testing.T) {
	// Given: an empty registry
	router, _ := newManifestRouter(t)

	// When: a digest that was never pushed is fetched
	rec := serve(t, router, http.MethodGet, "/v2/test/manifests/"+digestOf([]byte("nope")), nil)

	// Then: the manifest is unknown
	requireCode(t, rec, http.StatusNotFound, "MANIFEST_UNKNOWN")
}

func Test_GetManifest_when_UnknownTag_then_ManifestUnknown(t *testing.T) {
	// Given: a repository with no manifests
	router, _ := newManifestRouter(t)
	if rec := putManifest(t, router, "/v2/test/manifests/v1", imageManifestJSON(t, mediaTypeDockerManifest, digestOf([]byte("cfg"))), mediaTypeDockerManifest); rec.Code != http.StatusCreated {
		t.Fatalf("push status = %d, want 201", rec.Code)
	}

	// When: a tag that was never set is fetched
	rec := serve(t, router, http.MethodGet, "/v2/test/manifests/latest", nil)

	// Then: the manifest is unknown
	requireCode(t, rec, http.StatusNotFound, "MANIFEST_UNKNOWN")
}

func Test_GetManifest_when_UnknownRepository_then_ManifestUnknown(t *testing.T) {
	// Given: an empty registry
	router, _ := newManifestRouter(t)

	// When: any manifest of an unknown repository is fetched
	rec := serve(t, router, http.MethodGet, "/v2/nope/manifests/latest", nil)

	// Then: the manifest is unknown
	requireCode(t, rec, http.StatusNotFound, "MANIFEST_UNKNOWN")
}

func Test_GetManifest_when_InvalidName_then_NameInvalid(t *testing.T) {
	// Given: an empty registry
	router, _ := newManifestRouter(t)

	// When: a syntactically invalid repository name is used
	rec := serve(t, router, http.MethodGet, "/v2/UPPER/manifests/latest", nil)

	// Then: the name is refused before any lookup
	requireCode(t, rec, http.StatusBadRequest, "NAME_INVALID")
}

func Test_GetManifest_when_InvalidTag_then_TagInvalid(t *testing.T) {
	// Given: an empty registry
	router, _ := newManifestRouter(t)

	// When: a reference that is neither a digest nor a tag is used
	rec := serve(t, router, http.MethodGet, "/v2/test/manifests/not-a-tag!", nil)

	// Then: the tag is refused
	requireCode(t, rec, http.StatusBadRequest, "TAG_INVALID")
}

func Test_GetManifest_when_MalformedDigest_then_DigestInvalid(t *testing.T) {
	// Given: an empty registry
	router, _ := newManifestRouter(t)

	// When: a reference shaped like a digest carries a bad digest
	rec := serve(t, router, http.MethodGet, "/v2/test/manifests/sha256:nothex", nil)

	// Then: the digest is refused
	requireCode(t, rec, http.StatusBadRequest, "DIGEST_INVALID")
}
