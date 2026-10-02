package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func subjectManifestJSON(t *testing.T, subject, artifactType string) []byte {
	t.Helper()
	return marshalJSON(t, map[string]any{
		"schemaVersion": 2,
		"mediaType":     mediaTypeOCIManifest,
		"artifactType":  artifactType,
		"subject":       map[string]any{"mediaType": mediaTypeOCIManifest, "digest": subject, "size": 1},
		"config":        map[string]any{"mediaType": "application/vnd.oci.empty.v1+json", "digest": digestOf([]byte("cfg")), "size": 1},
		"layers":        []map[string]any{},
		"annotations":   map[string]string{"org.opencontainers.example": "hello"},
	})
}

func decodeIndex(t *testing.T, body []byte) struct {
	MediaType string `json:"mediaType"`
	Manifests []struct {
		MediaType    string            `json:"mediaType"`
		Digest       string            `json:"digest"`
		Size         int64             `json:"size"`
		ArtifactType string            `json:"artifactType"`
		Annotations  map[string]string `json:"annotations"`
	} `json:"manifests"`
} {
	t.Helper()
	var out struct {
		MediaType string `json:"mediaType"`
		Manifests []struct {
			MediaType    string            `json:"mediaType"`
			Digest       string            `json:"digest"`
			Size         int64             `json:"size"`
			ArtifactType string            `json:"artifactType"`
			Annotations  map[string]string `json:"annotations"`
		} `json:"manifests"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode referrers index: %v (body %s)", err, body)
	}
	return out
}

// Given: an empty registry. When: referrers of an unknown subject are listed.
// Then: 200 with an empty OCI index — never a 404.
func Test_Referrers_when_UnknownSubject_then_EmptyIndex(t *testing.T) {
	router, _ := newManifestRouter(t)
	rec := serve(t, router, http.MethodGet, "/v2/test/referrers/"+digestOf([]byte("absent")), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	assertHeader(t, rec.Header(), "Content-Type", mediaTypeOCIIndex)
	index := decodeIndex(t, rec.Body.Bytes())
	if index.MediaType != mediaTypeOCIIndex || len(index.Manifests) != 0 {
		t.Fatalf("index = %+v, want empty %s", index, mediaTypeOCIIndex)
	}
}

// Given: a manifest pushed with a subject. When: referrers of the subject are
// listed. Then: the manifest appears with its artifact type and annotations.
func Test_Referrers_when_SubjectPushed_then_Listed(t *testing.T) {
	router, _ := newManifestRouter(t)
	subject := digestOf([]byte("subject-manifest"))
	payload := subjectManifestJSON(t, subject, "application/vnd.example.artifact.v1")
	referrerDigest := digestOf(payload)
	pushed := putManifest(t, router, "/v2/test/manifests/tagged", payload, mediaTypeOCIManifest)
	if pushed.Code != http.StatusCreated {
		t.Fatalf("push status = %d, want 201 (body %s)", pushed.Code, pushed.Body.String())
	}
	if got := pushed.Header().Get("OCI-Subject"); got != subject {
		t.Fatalf("OCI-Subject = %q, want %q", got, subject)
	}

	rec := serve(t, router, http.MethodGet, "/v2/test/referrers/"+subject, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	index := decodeIndex(t, rec.Body.Bytes())
	if len(index.Manifests) != 1 {
		t.Fatalf("manifests = %d, want 1 (body %s)", len(index.Manifests), rec.Body.String())
	}
	entry := index.Manifests[0]
	if entry.Digest != referrerDigest || entry.ArtifactType != "application/vnd.example.artifact.v1" {
		t.Fatalf("entry = %+v, want digest %s with artifact type", entry, referrerDigest)
	}
	if entry.Annotations["org.opencontainers.example"] != "hello" {
		t.Fatalf("annotations = %v, want the manifest annotations", entry.Annotations)
	}
}

// Given: two referrers with different artifact types. When: the list is
// filtered by artifactType. Then: only matching entries return and the filter
// is reported as applied.
func Test_Referrers_when_ArtifactTypeFilter_then_Applied(t *testing.T) {
	router, _ := newManifestRouter(t)
	subject := digestOf([]byte("subject-manifest"))
	for _, artifactType := range []string{"application/vnd.example.a", "application/vnd.example.b"} {
		payload := subjectManifestJSON(t, subject, artifactType)
		if rec := putManifest(t, router, "/v2/test/manifests/"+digestOf(payload), payload, mediaTypeOCIManifest); rec.Code != http.StatusCreated {
			t.Fatalf("push %s status = %d, want 201", artifactType, rec.Code)
		}
	}

	rec := serve(t, router, http.MethodGet,
		"/v2/test/referrers/"+subject+"?artifactType=application/vnd.example.b", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	assertHeader(t, rec.Header(), "OCI-Filters-Applied", "artifactType")
	index := decodeIndex(t, rec.Body.Bytes())
	if len(index.Manifests) != 1 || index.Manifests[0].ArtifactType != "application/vnd.example.b" {
		t.Fatalf("manifests = %+v, want only artifact type b", index.Manifests)
	}
}
