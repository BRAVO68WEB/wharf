package api_test

import (
	"net/http"
	"testing"
)

func Test_GetManifest_when_AcceptOmitsMediaType_then_ManifestUnknown(t *testing.T) {
	// Given: a Docker schema2 manifest
	router, _ := newManifestRouter(t)
	payload := imageManifestJSON(t, mediaTypeDockerManifest, digestOf([]byte("cfg")))
	if rec := putManifest(t, router, "/v2/test/manifests/latest", payload, mediaTypeDockerManifest); rec.Code != http.StatusCreated {
		t.Fatalf("push status = %d, want 201", rec.Code)
	}

	// When: the client only accepts OCI manifests
	rec := serve(t, router, http.MethodGet, "/v2/test/manifests/latest", map[string]string{
		"Accept": mediaTypeOCIManifest,
	})

	// Then: the manifest is reported unknown rather than converted
	requireCode(t, rec, http.StatusNotFound, "MANIFEST_UNKNOWN")
}

func Test_GetManifest_when_AcceptListsMediaType_then_ReturnsManifest(t *testing.T) {
	// Given: a Docker schema2 manifest
	router, _ := newManifestRouter(t)
	payload := imageManifestJSON(t, mediaTypeDockerManifest, digestOf([]byte("cfg")))
	if rec := putManifest(t, router, "/v2/test/manifests/latest", payload, mediaTypeDockerManifest); rec.Code != http.StatusCreated {
		t.Fatalf("push status = %d, want 201", rec.Code)
	}

	// When: the client offers a list of media types including the stored one
	rec := serve(t, router, http.MethodGet, "/v2/test/manifests/latest", map[string]string{
		"Accept": mediaTypeOCIManifest + ", " + mediaTypeDockerManifest + ";q=0.9, " + mediaTypeDockerList,
	})

	// Then: the manifest is served
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	assertHeader(t, rec.Header(), "Content-Type", mediaTypeDockerManifest)
}

func Test_GetManifest_when_AcceptWildcard_then_ReturnsManifest(t *testing.T) {
	// Given: an OCI index
	router, _ := newManifestRouter(t)
	payload := indexManifestJSON(t, mediaTypeOCIIndex, digestOf([]byte("child")))
	if rec := putManifest(t, router, "/v2/test/manifests/multi", payload, mediaTypeOCIIndex); rec.Code != http.StatusCreated {
		t.Fatalf("push status = %d, want 201", rec.Code)
	}

	// When: the client accepts everything or everything of the type
	for _, accept := range []string{"*/*", "application/*"} {
		rec := serve(t, router, http.MethodGet, "/v2/test/manifests/multi", map[string]string{"Accept": accept})
		if rec.Code != http.StatusOK {
			t.Fatalf("Accept %q: status = %d, want %d", accept, rec.Code, http.StatusOK)
		}
	}
}

func Test_HeadManifest_when_AcceptOmitsMediaType_then_ManifestUnknown(t *testing.T) {
	// Given: a Docker schema2 manifest
	router, _ := newManifestRouter(t)
	payload := imageManifestJSON(t, mediaTypeDockerManifest, digestOf([]byte("cfg")))
	if rec := putManifest(t, router, "/v2/test/manifests/latest", payload, mediaTypeDockerManifest); rec.Code != http.StatusCreated {
		t.Fatalf("push status = %d, want 201", rec.Code)
	}

	// When: HEAD carries an Accept list without the stored type
	rec := serve(t, router, http.MethodHead, "/v2/test/manifests/latest", map[string]string{
		"Accept": mediaTypeOCIIndex,
	})

	// Then: negotiation fails the same way as for GET
	requireCode(t, rec, http.StatusNotFound, "MANIFEST_UNKNOWN")
}
