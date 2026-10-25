package api_test

import (
	"net/http"
	"testing"

	"github.com/BRAVO68WEB/wharf/internal/db"
)

func Test_PutManifest_when_ImageManifest_then_CreatesRepositoryAndManifest(t *testing.T) {
	// Given: an empty registry
	router, q := newManifestRouter(t)
	payload := imageManifestJSON(t, mediaTypeDockerManifest,
		digestOf([]byte("cfg")), digestOf([]byte("l1")), digestOf([]byte("l2")))
	digest := digestOf(payload)

	// When: the manifest is pushed under a tag
	rec := putManifest(t, router, "/v2/test/manifests/latest", payload, mediaTypeDockerManifest)

	// Then: the push is accepted and the payload lands in the metadata store
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	assertHeader(t, rec.Header(), "Docker-Content-Digest", digest)
	assertHeader(t, rec.Header(), "Location", "/v2/test/manifests/"+digest)
	assertAPIVersion(t, rec)

	repo, err := q.GetRepositoryByName(t.Context(), "test")
	if err != nil {
		t.Fatalf("repository: %v", err)
	}
	m, err := q.GetManifest(t.Context(), db.GetManifestParams{RepoID: repo.ID, Digest: digest})
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if m.MediaType != mediaTypeDockerManifest {
		t.Fatalf("media type = %q, want %q", m.MediaType, mediaTypeDockerManifest)
	}
	if string(m.Payload) != string(payload) {
		t.Fatalf("stored payload = %q, want %q", m.Payload, payload)
	}
	if m.Size != int64(len(payload)) {
		t.Fatalf("size = %d, want %d", m.Size, len(payload))
	}
}

func Test_PutManifest_when_ImageManifest_then_RegistersBlobRefs(t *testing.T) {
	// Given: a manifest with a config and two layers
	router, q := newManifestRouter(t)
	config, l1, l2 := digestOf([]byte("cfg")), digestOf([]byte("l1")), digestOf([]byte("l2"))
	payload := imageManifestJSON(t, mediaTypeDockerManifest, config, l1, l2)

	// When: it is pushed
	if rec := putManifest(t, router, "/v2/test/manifests/latest", payload, mediaTypeDockerManifest); rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}

	// Then: every referenced blob is registered for the repository
	repo, err := q.GetRepositoryByName(t.Context(), "test")
	if err != nil {
		t.Fatalf("repository: %v", err)
	}
	for _, d := range []string{config, l1, l2} {
		if _, err := q.GetBlobRef(t.Context(), db.GetBlobRefParams{Digest: d, RepoID: repo.ID}); err != nil {
			t.Fatalf("blob ref %s: %v", d, err)
		}
	}
}

func Test_PutManifest_when_Index_then_RegistersEntryRefs(t *testing.T) {
	// Given: an index pointing at two child manifests
	router, q := newManifestRouter(t)
	c1, c2 := digestOf([]byte("child1")), digestOf([]byte("child2"))
	payload := indexManifestJSON(t, mediaTypeDockerList, c1, c2)

	// When: it is pushed
	if rec := putManifest(t, router, "/v2/test/manifests/multi", payload, mediaTypeDockerList); rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}

	// Then: the child digests are registered as references of the repository
	repo, err := q.GetRepositoryByName(t.Context(), "test")
	if err != nil {
		t.Fatalf("repository: %v", err)
	}
	for _, d := range []string{c1, c2} {
		if _, err := q.GetBlobRef(t.Context(), db.GetBlobRefParams{Digest: d, RepoID: repo.ID}); err != nil {
			t.Fatalf("blob ref %s: %v", d, err)
		}
	}
}

func Test_PutManifest_when_TagReference_then_SetsTag(t *testing.T) {
	// Given: an empty registry
	router, q := newManifestRouter(t)
	payload := imageManifestJSON(t, mediaTypeDockerManifest, digestOf([]byte("cfg")))
	digest := digestOf(payload)

	// When: the manifest is pushed under a tag
	if rec := putManifest(t, router, "/v2/test/manifests/1.0", payload, mediaTypeDockerManifest); rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}

	// Then: the tag points at the manifest digest
	repo, err := q.GetRepositoryByName(t.Context(), "test")
	if err != nil {
		t.Fatalf("repository: %v", err)
	}
	tag, err := q.GetTag(t.Context(), db.GetTagParams{RepoID: repo.ID, Name: "1.0"})
	if err != nil {
		t.Fatalf("tag: %v", err)
	}
	if tag.ManifestDigest != digest {
		t.Fatalf("tag digest = %q, want %q", tag.ManifestDigest, digest)
	}
}

func Test_PutManifest_when_DigestReference_then_SetsNoTag(t *testing.T) {
	// Given: an empty registry
	router, q := newManifestRouter(t)
	payload := imageManifestJSON(t, mediaTypeDockerManifest, digestOf([]byte("cfg")))
	digest := digestOf(payload)

	// When: the manifest is pushed under its digest
	if rec := putManifest(t, router, "/v2/test/manifests/"+digest, payload, mediaTypeDockerManifest); rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}

	// Then: the manifest is stored but no tag is created
	repo, err := q.GetRepositoryByName(t.Context(), "test")
	if err != nil {
		t.Fatalf("repository: %v", err)
	}
	if _, err := q.GetTag(t.Context(), db.GetTagParams{RepoID: repo.ID, Name: digest}); err == nil {
		t.Fatal("digest push created a tag")
	}
	if n, err := q.CountTagsByRepo(t.Context(), repo.ID); err != nil || n != 0 {
		t.Fatalf("tag count = %d (err %v), want 0", n, err)
	}
}

func Test_PutManifest_when_OCIManifest_then_StoresOCIMediaType(t *testing.T) {
	// Given: an OCI image manifest
	router, _ := newManifestRouter(t)
	payload := imageManifestJSON(t, mediaTypeOCIManifest, digestOf([]byte("cfg")))

	// When: it is pushed and fetched
	if rec := putManifest(t, router, "/v2/test/manifests/latest", payload, mediaTypeOCIManifest); rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	got := serve(t, router, http.MethodGet, "/v2/test/manifests/latest", nil)

	// Then: the OCI media type is served back
	assertManifestHeaders(t, got, digestOf(payload), mediaTypeOCIManifest, len(payload))
}

func Test_PutManifest_when_NoContentType_then_InfersFromPayload(t *testing.T) {
	// Given: a manifest whose payload declares its media type
	router, _ := newManifestRouter(t)
	payload := imageManifestJSON(t, mediaTypeOCIManifest, digestOf([]byte("cfg")))

	// When: it is pushed without a Content-Type header
	rec := putManifest(t, router, "/v2/test/manifests/latest", payload, "")

	// Then: the payload's media type is used
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	got := serve(t, router, http.MethodGet, "/v2/test/manifests/latest", nil)
	assertManifestHeaders(t, got, digestOf(payload), mediaTypeOCIManifest, len(payload))
}

func Test_PutManifest_when_NoMediaTypeAnywhere_then_InfersImageManifest(t *testing.T) {
	// Given: a schema2-shaped payload with no media type at all
	router, _ := newManifestRouter(t)
	payload := imageManifestJSON(t, "", digestOf([]byte("cfg")))

	// When: it is pushed with a generic Content-Type
	rec := putManifest(t, router, "/v2/test/manifests/latest", payload, "application/json")

	// Then: it is stored as a Docker schema2 manifest
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	got := serve(t, router, http.MethodGet, "/v2/test/manifests/latest", nil)
	assertManifestHeaders(t, got, digestOf(payload), mediaTypeDockerManifest, len(payload))
}
