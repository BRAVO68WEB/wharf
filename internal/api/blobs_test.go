package api_test

import (
	"bytes"
	"fmt"
	"net/http"
	"testing"

	"github.com/user/better-docker-registry/internal/api"
	"github.com/user/better-docker-registry/internal/storage"
)

func Test_GetBlob_when_Exists_then_StreamsExactBytesWithHeaders(t *testing.T) {
	// Given: a stored 1000-byte blob
	store := newFakeStorage()
	data := bytes.Repeat([]byte("0123456789"), 100)
	digest := store.seed(data)
	router := api.NewRouter(store, newTestQueries(t))

	// When: the blob is fetched
	rec := serve(t, router, http.MethodGet, "/v2/test/blobs/"+digest, nil)

	// Then: the exact bytes come back with the registry headers
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !bytes.Equal(rec.Body.Bytes(), data) {
		t.Fatalf("body = %d bytes, want %d identical bytes", rec.Body.Len(), len(data))
	}
	assertHeader(t, rec.Header(), "Docker-Content-Digest", digest)
	assertHeader(t, rec.Header(), "Content-Length", "1000")
	assertHeader(t, rec.Header(), "Accept-Ranges", "bytes")
	assertHeader(t, rec.Header(), "ETag", `"`+digest+`"`)
	assertHeader(t, rec.Header(), "Content-Type", "application/octet-stream")
	assertHeader(t, rec.Header(), storage.HeaderAPIVersion, storage.APIVersion)
}

func Test_GetBlob_when_NestedRepoName_then_StreamsBytes(t *testing.T) {
	// Given: a stored blob addressed by a repository name with slashes
	store := newFakeStorage()
	data := []byte("nested")
	digest := store.seed(data)
	router := api.NewRouter(store, newTestQueries(t))

	// When: the blob is fetched under library/nginx
	rec := serve(t, router, http.MethodGet, "/v2/library/nginx/blobs/"+digest, nil)

	// Then: it is served like any other name
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !bytes.Equal(rec.Body.Bytes(), data) {
		t.Fatalf("body = %q, want %q", rec.Body.Bytes(), data)
	}
}

func Test_GetBlob_when_NameContainsOpSegment_then_StreamsBytes(t *testing.T) {
	// Given: a repository whose name contains a "blobs" segment
	store := newFakeStorage()
	data := []byte("op-in-name")
	digest := store.seed(data)
	router := api.NewRouter(store, newTestQueries(t))

	// When: its blob is fetched
	rec := serve(t, router, http.MethodGet, "/v2/acme/blobs/blobs/"+digest, nil)

	// Then: the name keeps its "blobs" segment and the blob is served
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !bytes.Equal(rec.Body.Bytes(), data) {
		t.Fatalf("body = %q, want %q", rec.Body.Bytes(), data)
	}
}

func Test_GetBlob_when_BlobMissing_then_404BlobUnknown(t *testing.T) {
	// Given: an empty store
	store := newFakeStorage()
	router := api.NewRouter(store, newTestQueries(t))
	digest := digestOf([]byte("absent"))

	// When: a blob the registry does not hold is fetched
	rec := serve(t, router, http.MethodGet, "/v2/test/blobs/"+digest, nil)

	// Then: 404 with the byte-exact error contract
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	want := `{"errors":[{"code":"BLOB_UNKNOWN","message":"blob unknown to the registry","detail":{}}]}`
	if rec.Body.String() != want {
		t.Fatalf("body = %s, want %s", rec.Body.String(), want)
	}
	assertHeader(t, rec.Header(), "Content-Type", "application/json")
	assertHeader(t, rec.Header(), storage.HeaderAPIVersion, storage.APIVersion)
}

func Test_GetBlob_when_DigestMalformed_then_400DigestInvalid(t *testing.T) {
	// Given: a router over an empty store
	router := api.NewRouter(newFakeStorage(), newTestQueries(t))

	// When: the digest is not a sha256 digest
	rec := serve(t, router, http.MethodGet, "/v2/test/blobs/sha256:nothex", nil)

	// Then: 400 DIGEST_INVALID
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if code := decodeErrors(t, rec).Errors[0].Code; code != storage.ErrorCodeDigestInvalid {
		t.Fatalf("error code = %s, want %s", code, storage.ErrorCodeDigestInvalid)
	}
}

func Test_GetBlob_when_NameMalformed_then_400NameInvalid(t *testing.T) {
	// Given: a router over an empty store
	router := api.NewRouter(newFakeStorage(), newTestQueries(t))
	digest := digestOf([]byte("anything"))

	// When: the repository name is not a valid OCI name
	rec := serve(t, router, http.MethodGet, "/v2/Not_Valid/blobs/"+digest, nil)

	// Then: 400 NAME_INVALID
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if code := decodeErrors(t, rec).Errors[0].Code; code != storage.ErrorCodeNameInvalid {
		t.Fatalf("error code = %s, want %s", code, storage.ErrorCodeNameInvalid)
	}
}

func Test_GetBlob_when_StorageUnavailable_then_503Unavailable(t *testing.T) {
	// Given: a storage backend that cannot serve the request
	store := newFakeStorage()
	store.fail = fmt.Errorf("disk on fire: %w", storage.ErrStorageUnavailable)
	router := api.NewRouter(store, newTestQueries(t))
	digest := digestOf([]byte("unreachable"))

	// When: a blob is fetched
	rec := serve(t, router, http.MethodGet, "/v2/test/blobs/"+digest, nil)

	// Then: 503 UNAVAILABLE
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if code := decodeErrors(t, rec).Errors[0].Code; code != storage.ErrorCodeUnavailable {
		t.Fatalf("error code = %s, want %s", code, storage.ErrorCodeUnavailable)
	}
}

func Test_HeadBlob_when_Exists_then_HeadersWithoutBody(t *testing.T) {
	// Given: a stored 1000-byte blob
	store := newFakeStorage()
	data := bytes.Repeat([]byte("x"), 1000)
	digest := store.seed(data)
	router := api.NewRouter(store, newTestQueries(t))

	// When: the blob is headed
	rec := serve(t, router, http.MethodHead, "/v2/test/blobs/"+digest, nil)

	// Then: the headers describe the blob and no body is written
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("body = %q, want empty", rec.Body.String())
	}
	assertHeader(t, rec.Header(), "Docker-Content-Digest", digest)
	assertHeader(t, rec.Header(), "Content-Length", "1000")
	assertHeader(t, rec.Header(), "Accept-Ranges", "bytes")
	assertHeader(t, rec.Header(), storage.HeaderAPIVersion, storage.APIVersion)
}

func Test_HeadBlob_when_BlobMissing_then_404BlobUnknown(t *testing.T) {
	// Given: an empty store
	router := api.NewRouter(newFakeStorage(), newTestQueries(t))
	digest := digestOf([]byte("absent"))

	// When: a missing blob is headed
	rec := serve(t, router, http.MethodHead, "/v2/test/blobs/"+digest, nil)

	// Then: 404 with the registry error code
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if code := decodeErrors(t, rec).Errors[0].Code; code != storage.ErrorCodeBlobUnknown {
		t.Fatalf("error code = %s, want %s", code, storage.ErrorCodeBlobUnknown)
	}
}

func Test_DeleteBlob_when_Exists_then_202AndBlobGone(t *testing.T) {
	// Given: a stored blob
	store := newFakeStorage()
	digest := store.seed([]byte("delete me"))
	router := api.NewRouter(store, newTestQueries(t))

	// When: the blob is deleted
	rec := serve(t, router, http.MethodDelete, "/v2/test/blobs/"+digest, nil)

	// Then: 202 Accepted and the blob is gone
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	assertHeader(t, rec.Header(), "Docker-Content-Digest", digest)
	assertHeader(t, rec.Header(), storage.HeaderAPIVersion, storage.APIVersion)

	again := serve(t, router, http.MethodGet, "/v2/test/blobs/"+digest, nil)
	if again.Code != http.StatusNotFound {
		t.Fatalf("GET after DELETE status = %d, want %d", again.Code, http.StatusNotFound)
	}
}

func Test_DeleteBlob_when_BlobMissing_then_404BlobUnknown(t *testing.T) {
	// Given: an empty store
	router := api.NewRouter(newFakeStorage(), newTestQueries(t))
	digest := digestOf([]byte("never stored"))

	// When: a missing blob is deleted
	rec := serve(t, router, http.MethodDelete, "/v2/test/blobs/"+digest, nil)

	// Then: 404 BLOB_UNKNOWN, not a silent success
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if code := decodeErrors(t, rec).Errors[0].Code; code != storage.ErrorCodeBlobUnknown {
		t.Fatalf("error code = %s, want %s", code, storage.ErrorCodeBlobUnknown)
	}
}
