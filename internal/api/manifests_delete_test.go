package api_test

import (
	"net/http"
	"testing"
)

func Test_DeleteManifest_when_Digest_then_RemovesManifestAndTags(t *testing.T) {
	// Given: a pushed manifest tagged twice
	router, _ := newManifestRouter(t)
	payload := imageManifestJSON(t, mediaTypeDockerManifest, digestOf([]byte("cfg")))
	digest := digestOf(payload)
	for _, tag := range []string{"latest", "1.0"} {
		if rec := putManifest(t, router, "/v2/test/manifests/"+tag, payload, mediaTypeDockerManifest); rec.Code != http.StatusCreated {
			t.Fatalf("push %s status = %d, want 201", tag, rec.Code)
		}
	}

	// When: the manifest is deleted by digest
	rec := serve(t, router, http.MethodDelete, "/v2/test/manifests/"+digest, nil)

	// Then: the delete is accepted and the manifest and its tags are gone
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	assertHeader(t, rec.Header(), "Docker-Content-Digest", digest)
	requireCode(t, serve(t, router, http.MethodGet, "/v2/test/manifests/"+digest, nil),
		http.StatusNotFound, "MANIFEST_UNKNOWN")
	for _, tag := range []string{"latest", "1.0"} {
		requireCode(t, serve(t, router, http.MethodGet, "/v2/test/manifests/"+tag, nil),
			http.StatusNotFound, "MANIFEST_UNKNOWN")
	}
}

func Test_DeleteManifest_when_Tag_then_UnmapsTagOnly(t *testing.T) {
	// Given: a pushed manifest under two tags
	router, _ := newManifestRouter(t)
	payload := imageManifestJSON(t, mediaTypeDockerManifest, digestOf([]byte("cfg")))
	digest := digestOf(payload)
	for _, tag := range []string{"latest", "1.0"} {
		if rec := putManifest(t, router, "/v2/test/manifests/"+tag, payload, mediaTypeDockerManifest); rec.Code != http.StatusCreated {
			t.Fatalf("push %s status = %d, want 201", tag, rec.Code)
		}
	}

	// When: deletion is requested by tag
	rec := serve(t, router, http.MethodDelete, "/v2/test/manifests/latest", nil)

	// Then: the tag is unmapped and the manifest (and its other tag) survive
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	requireCode(t, serve(t, router, http.MethodGet, "/v2/test/manifests/latest", nil),
		http.StatusNotFound, "MANIFEST_UNKNOWN")
	got := serve(t, router, http.MethodGet, "/v2/test/manifests/"+digest, nil)
	if got.Code != http.StatusOK {
		t.Fatalf("manifest status = %d, want 200 (tag delete must not remove it)", got.Code)
	}
	other := serve(t, router, http.MethodGet, "/v2/test/manifests/1.0", nil)
	if other.Code != http.StatusOK {
		t.Fatalf("other tag status = %d, want 200", other.Code)
	}
}

func Test_DeleteManifest_when_Unknown_then_ManifestUnknown(t *testing.T) {
	// Given: an empty registry
	router, _ := newManifestRouter(t)

	// When: a digest that does not exist is deleted
	rec := serve(t, router, http.MethodDelete, "/v2/test/manifests/"+digestOf([]byte("nope")), nil)

	// Then: the manifest is unknown
	requireCode(t, rec, http.StatusNotFound, "MANIFEST_UNKNOWN")
}

func Test_DeleteManifest_when_InvalidReference_then_Refused(t *testing.T) {
	// Given: an empty registry
	router, _ := newManifestRouter(t)

	// When: malformed references are deleted
	requireCode(t, serve(t, router, http.MethodDelete, "/v2/test/manifests/sha256:nothex", nil),
		http.StatusBadRequest, "DIGEST_INVALID")
	requireCode(t, serve(t, router, http.MethodDelete, "/v2/test/manifests/bad!tag", nil),
		http.StatusBadRequest, "TAG_INVALID")
	requireCode(t, serve(t, router, http.MethodDelete, "/v2/UPPER/manifests/"+digestOf([]byte("x")), nil),
		http.StatusBadRequest, "NAME_INVALID")
}
