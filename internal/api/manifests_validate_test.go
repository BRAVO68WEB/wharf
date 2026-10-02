package api_test

import (
	"bytes"
	"net/http"
	"testing"
)

func Test_PutManifest_when_Schema1_then_ManifestInvalid(t *testing.T) {
	// Given: a deprecated Docker schema1 manifest
	router, _ := newManifestRouter(t)
	payload := schema1ManifestJSON(t)

	// When: it is pushed
	rec := putManifest(t, router, "/v2/test/manifests/latest", payload, mediaTypeSchema1)

	// Then: the push is refused as an invalid manifest
	requireCode(t, rec, http.StatusBadRequest, "MANIFEST_INVALID")
}

func Test_PutManifest_when_SchemaVersionOne_then_ManifestInvalid(t *testing.T) {
	// Given: a payload with schemaVersion 1 and no recognised media type
	router, _ := newManifestRouter(t)
	payload := marshalJSON(t, map[string]any{"schemaVersion": 1, "fsLayers": []any{}})

	// When: it is pushed
	rec := putManifest(t, router, "/v2/test/manifests/latest", payload, "application/json")

	// Then: the push is refused as an invalid manifest
	requireCode(t, rec, http.StatusBadRequest, "MANIFEST_INVALID")
}

func Test_PutManifest_when_DigestMismatch_then_DigestInvalid(t *testing.T) {
	// Given: a manifest whose content does not hash to the pushed digest
	router, _ := newManifestRouter(t)
	payload := imageManifestJSON(t, mediaTypeDockerManifest, digestOf([]byte("cfg")))

	// When: it is pushed under someone else's digest
	rec := putManifest(t, router, "/v2/test/manifests/"+digestOf([]byte("other")), payload, mediaTypeDockerManifest)

	// Then: the digest is refused and nothing is stored
	requireCode(t, rec, http.StatusBadRequest, "DIGEST_INVALID")
}

func Test_PutManifest_when_MissingConfig_then_ManifestInvalid(t *testing.T) {
	// Given: an image manifest with layers but no config
	router, _ := newManifestRouter(t)
	payload := marshalJSON(t, map[string]any{
		"schemaVersion": 2,
		"mediaType":     mediaTypeDockerManifest,
		"layers":        []map[string]any{{"mediaType": layerMediaType, "digest": digestOf([]byte("l1")), "size": 1}},
	})

	// When: it is pushed
	rec := putManifest(t, router, "/v2/test/manifests/latest", payload, mediaTypeDockerManifest)

	// Then: the push is refused as an invalid manifest
	requireCode(t, rec, http.StatusBadRequest, "MANIFEST_INVALID")
}

func Test_PutManifest_when_MissingLayers_then_ManifestInvalid(t *testing.T) {
	// Given: an image manifest with a config but no layers field at all
	router, _ := newManifestRouter(t)
	payload := marshalJSON(t, map[string]any{
		"schemaVersion": 2,
		"mediaType":     mediaTypeDockerManifest,
		"config":        map[string]any{"mediaType": configMediaType, "digest": digestOf([]byte("cfg")), "size": 1},
	})

	// When: it is pushed
	rec := putManifest(t, router, "/v2/test/manifests/latest", payload, mediaTypeDockerManifest)

	// Then: the push is refused as an invalid manifest
	requireCode(t, rec, http.StatusBadRequest, "MANIFEST_INVALID")
}

func Test_PutManifest_when_EmptyLayers_then_Accepted(t *testing.T) {
	// Given: an image manifest with an empty layer list
	router, _ := newManifestRouter(t)
	payload := imageManifestJSON(t, mediaTypeDockerManifest, digestOf([]byte("cfg")))

	// When: it is pushed
	rec := putManifest(t, router, "/v2/test/manifests/latest", payload, mediaTypeDockerManifest)

	// Then: an empty layers array is valid
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
}

func Test_PutManifest_when_BadLayerDigest_then_ManifestInvalid(t *testing.T) {
	// Given: an image manifest whose layer digest is malformed
	router, _ := newManifestRouter(t)
	payload := marshalJSON(t, map[string]any{
		"schemaVersion": 2,
		"mediaType":     mediaTypeDockerManifest,
		"config":        map[string]any{"mediaType": configMediaType, "digest": digestOf([]byte("cfg")), "size": 1},
		"layers":        []map[string]any{{"mediaType": layerMediaType, "digest": "sha256:nothex", "size": 1}},
	})

	// When: it is pushed
	rec := putManifest(t, router, "/v2/test/manifests/latest", payload, mediaTypeDockerManifest)

	// Then: the push is refused as an invalid manifest
	requireCode(t, rec, http.StatusBadRequest, "MANIFEST_INVALID")
}

func Test_PutManifest_when_IndexMissingManifests_then_ManifestInvalid(t *testing.T) {
	// Given: an index without a manifests array
	router, _ := newManifestRouter(t)
	payload := marshalJSON(t, map[string]any{"schemaVersion": 2, "mediaType": mediaTypeOCIIndex})

	// When: it is pushed
	rec := putManifest(t, router, "/v2/test/manifests/multi", payload, mediaTypeOCIIndex)

	// Then: the push is refused as an invalid manifest
	requireCode(t, rec, http.StatusBadRequest, "MANIFEST_INVALID")
}

func Test_PutManifest_when_NotJSON_then_ManifestInvalid(t *testing.T) {
	// Given: a body that is not JSON
	router, _ := newManifestRouter(t)

	// When: it is pushed
	rec := putManifest(t, router, "/v2/test/manifests/latest", []byte("not json"), mediaTypeDockerManifest)

	// Then: the push is refused as an invalid manifest
	requireCode(t, rec, http.StatusBadRequest, "MANIFEST_INVALID")
}

func Test_PutManifest_when_UnsupportedMediaType_then_ManifestInvalid(t *testing.T) {
	// Given: a payload of an unrecognised shape and media type
	router, _ := newManifestRouter(t)
	payload := marshalJSON(t, map[string]any{"schemaVersion": 2, "something": "else"})

	// When: it is pushed under an unrecognised Content-Type
	rec := putManifest(t, router, "/v2/test/manifests/latest", payload, "application/octet-stream")

	// Then: the push is refused as an invalid manifest
	requireCode(t, rec, http.StatusBadRequest, "MANIFEST_INVALID")
}

func Test_PutManifest_when_Oversize_then_SizeInvalid(t *testing.T) {
	// Given: a manifest payload larger than the accepted maximum
	router, _ := newManifestRouter(t)
	big := bytes.Repeat([]byte("a"), 4<<20+1)

	// When: it is pushed
	rec := putManifest(t, router, "/v2/test/manifests/latest", big, mediaTypeDockerManifest)

	// Then: the size is refused
	requireCode(t, rec, http.StatusBadRequest, "SIZE_INVALID")
}

func Test_PutManifest_when_InvalidTag_then_TagInvalid(t *testing.T) {
	// Given: a valid manifest
	router, _ := newManifestRouter(t)
	payload := imageManifestJSON(t, mediaTypeDockerManifest, digestOf([]byte("cfg")))

	// When: it is pushed under a malformed tag
	rec := putManifest(t, router, "/v2/test/manifests/bad!tag", payload, mediaTypeDockerManifest)

	// Then: the tag is refused before the body is read
	requireCode(t, rec, http.StatusBadRequest, "TAG_INVALID")
}

func Test_PutManifest_when_InvalidName_then_NameInvalid(t *testing.T) {
	// Given: a valid manifest
	router, _ := newManifestRouter(t)
	payload := imageManifestJSON(t, mediaTypeDockerManifest, digestOf([]byte("cfg")))

	// When: it is pushed to a malformed repository name
	rec := putManifest(t, router, "/v2/UPPER/manifests/latest", payload, mediaTypeDockerManifest)

	// Then: the name is refused
	requireCode(t, rec, http.StatusBadRequest, "NAME_INVALID")
}
