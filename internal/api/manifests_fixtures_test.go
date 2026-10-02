package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// Manifest media types used by the fixtures. The wire constants live in the
// api package; these mirror them for expectations in assertions.
const (
	mediaTypeDockerManifest = "application/vnd.docker.distribution.manifest.v2+json"
	mediaTypeDockerList     = "application/vnd.docker.distribution.manifest.list.v2+json"
	mediaTypeOCIManifest    = "application/vnd.oci.image.manifest.v1+json"
	mediaTypeOCIIndex       = "application/vnd.oci.image.index.v1+json"
	mediaTypeSchema1        = "application/vnd.docker.distribution.manifest.v1+json"
)

const (
	layerMediaType  = "application/vnd.oci.image.layer.v1.tar+gzip"
	configMediaType = "application/vnd.oci.image.config.v1+json"
)

// imageManifestJSON builds an image manifest (Docker schema2 or OCI) whose
// config and layers are described by the given digests. An empty
// layerDigests slice yields a valid empty layer list, which the OCI
// conformance suite pushes as an image with no layers.
func imageManifestJSON(t *testing.T, mediaType, configDigest string, layerDigests ...string) []byte {
	t.Helper()
	layers := make([]map[string]any, 0, len(layerDigests))
	for _, d := range layerDigests {
		layers = append(layers, map[string]any{
			"mediaType": layerMediaType,
			"digest":    d,
			"size":      1,
		})
	}
	return marshalJSON(t, map[string]any{
		"schemaVersion": 2,
		"mediaType":     mediaType,
		"config": map[string]any{
			"mediaType": configMediaType,
			"digest":    configDigest,
			"size":      1,
		},
		"layers": layers,
	})
}

// indexManifestJSON builds a manifest list / image index pointing at the
// given child manifests.
func indexManifestJSON(t *testing.T, mediaType string, childDigests ...string) []byte {
	t.Helper()
	entries := make([]map[string]any, 0, len(childDigests))
	for _, d := range childDigests {
		entries = append(entries, map[string]any{
			"mediaType": mediaTypeDockerManifest,
			"digest":    d,
			"size":      1,
			"platform":  map[string]any{"architecture": "amd64", "os": "linux"},
		})
	}
	return marshalJSON(t, map[string]any{
		"schemaVersion": 2,
		"mediaType":     mediaType,
		"manifests":     entries,
	})
}

// schema1ManifestJSON builds the shape of a deprecated Docker schema1
// manifest, which the registry must refuse.
func schema1ManifestJSON(t *testing.T) []byte {
	t.Helper()
	return marshalJSON(t, map[string]any{
		"schemaVersion": 1,
		"name":          "test",
		"tag":           "latest",
		"architecture":  "amd64",
		"fsLayers": []map[string]any{
			{"blobSum": "sha256:" + "a" + string(bytes.Repeat([]byte("0"), 63))},
		},
		"history": []map[string]any{{"v1Compatibility": "{}"}},
	})
}

func marshalJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return b
}

// serveBody runs one request with a body through the registry route tree.
func serveBody(t *testing.T, h http.Handler, method, path string, body []byte, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// putManifest pushes a manifest payload as a request body with the given
// Content-Type ("" sends no header) and returns the recorder.
func putManifest(t *testing.T, h http.Handler, path string, body []byte, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	header := map[string]string{}
	if contentType != "" {
		header["Content-Type"] = contentType
	}
	return serveBody(t, h, http.MethodPut, path, body, header)
}

// requireCode fails the test unless the response carries the registry error
// contract with exactly the expected code.
func requireCode(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, wantStatus, rec.Body.String())
	}
	if got := decodeErrors(t, rec).Errors[0].Code; string(got) != wantCode {
		t.Fatalf("error code = %q, want %q (body %s)", got, wantCode, rec.Body.String())
	}
}

// assertAPIVersion checks the registry version header every /v2 response
// carries.
func assertAPIVersion(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	assertHeader(t, rec.Header(), "Docker-Distribution-API-Version", "registry/2.0")
}

// assertManifestHeaders checks the headers a manifest response carries
// besides Content-Type.
func assertManifestHeaders(t *testing.T, rec *httptest.ResponseRecorder, digest, mediaType string, size int) {
	t.Helper()
	assertHeader(t, rec.Header(), "Docker-Content-Digest", digest)
	assertHeader(t, rec.Header(), "Content-Type", mediaType)
	assertHeader(t, rec.Header(), "Content-Length", strconv.Itoa(size))
	assertAPIVersion(t, rec)
}
