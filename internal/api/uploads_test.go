package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/user/better-docker-registry/internal/api"
	"github.com/user/better-docker-registry/internal/db"
)

func uploadTestRouter(t *testing.T) (http.Handler, db.Querier, *fakeStorage) {
	t.Helper()
	q := newTestQueries(t)
	store := newFakeStorage()
	return api.NewRouter(store, q), q, store
}

func uploadTestBody(t *testing.T, h http.Handler, method, path string, body string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	return serveBody(t, h, method, path, []byte(body), header)
}

func uploadTestSession(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := serve(t, h, http.MethodPost, "/v2/test/blobs/uploads/", nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("initiate status = %d, want 202 (body %s)", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "/blobs/uploads/") {
		t.Fatalf("initiate Location = %q, want an upload session URL", loc)
	}
	return loc
}

// Given: an empty registry. When: a session is opened. Then: 202 with a
// resumable session Location and upload UUID.
func Test_UploadInitiate_when_Opened_then_SessionAccepted(t *testing.T) {
	h, _, _ := uploadTestRouter(t)
	rec := serve(t, h, http.MethodPost, "/v2/test/blobs/uploads/", nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body %s)", rec.Code, rec.Body.String())
	}
	assertHeader(t, rec.Header(), "Location", "/v2/test/blobs/uploads/"+rec.Header().Get("Docker-Upload-UUID"))
	if rec.Header().Get("Docker-Upload-UUID") == "" {
		t.Fatal("missing Docker-Upload-UUID")
	}
	assertAPIVersion(t, rec)
}

// Given: an open session. When: one chunk is PATCHed and PUT commits with the
// right digest. Then: 201 and the blob is retrievable.
func Test_UploadChunked_when_Committed_then_BlobCreated(t *testing.T) {
	h, _, _ := uploadTestRouter(t)
	loc := uploadTestSession(t, h)

	rec := uploadTestBody(t, h, http.MethodPatch, loc, "hello ", map[string]string{
		"Content-Range": "0-5",
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("patch status = %d, want 202 (body %s)", rec.Code, rec.Body.String())
	}
	loc = rec.Header().Get("Location")

	rec = uploadTestBody(t, h, http.MethodPut, loc+"?digest="+digestOf([]byte("hello world")), "world", map[string]string{
		"Content-Range": "6-10",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("put status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	assertHeader(t, rec.Header(), "Docker-Content-Digest", digestOf([]byte("hello world")))
	assertHeader(t, rec.Header(), "Location", "/v2/test/blobs/"+digestOf([]byte("hello world")))

	got := serve(t, h, http.MethodGet, "/v2/test/blobs/"+digestOf([]byte("hello world")), nil)
	if got.Code != http.StatusOK || got.Body.String() != "hello world" {
		t.Fatalf("blob get = %d %q, want 200 %q", got.Code, got.Body.String(), "hello world")
	}
}

// Given: an open session with bytes received. When: a chunk arrives out of
// order. Then: 416 and the session offset is unchanged.
func Test_UploadChunked_when_OutOfOrderChunk_then_RangeNotSatisfiable(t *testing.T) {
	h, _, _ := uploadTestRouter(t)
	loc := uploadTestSession(t, h)

	rec := uploadTestBody(t, h, http.MethodPatch, loc, "hello", map[string]string{
		"Content-Range": "0-4",
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("patch status = %d, want 202", rec.Code)
	}
	rec = uploadTestBody(t, h, http.MethodPatch, loc, "jumps", map[string]string{
		"Content-Range": "10-14",
	})
	if rec.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("out of order patch status = %d, want 416", rec.Code)
	}

	status := serve(t, h, http.MethodGet, loc, nil)
	if status.Code != http.StatusNoContent {
		t.Fatalf("status get = %d, want 204", status.Code)
	}
	assertHeader(t, status.Header(), "Range", "0-4")
}

// Given: an open session with content. When: PUT commits with a digest the
// content does not hash to. Then: 400 DIGEST_INVALID.
func Test_UploadChunked_when_DigestMismatch_then_DigestInvalid(t *testing.T) {
	h, _, _ := uploadTestRouter(t)
	loc := uploadTestSession(t, h)

	rec := uploadTestBody(t, h, http.MethodPut, loc+"?digest="+digestOf([]byte("wanted")), "other", nil)
	requireCode(t, rec, http.StatusBadRequest, "DIGEST_INVALID")
}

// Given: an empty registry. When: a monolithic POST carries the whole blob.
// Then: 201 and the blob is retrievable.
func Test_UploadMonolithic_when_Posted_then_BlobCreated(t *testing.T) {
	h, _, _ := uploadTestRouter(t)
	body := "one shot"
	rec := uploadTestBody(t, h, http.MethodPost, "/v2/test/blobs/uploads/?digest="+digestOf([]byte(body)), body, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	got := serve(t, h, http.MethodGet, "/v2/test/blobs/"+digestOf([]byte(body)), nil)
	if got.Code != http.StatusOK || got.Body.String() != body {
		t.Fatalf("blob get = %d %q, want 200 %q", got.Code, got.Body.String(), body)
	}
}

// Given: an empty registry. When: a monolithic POST carries content that does
// not match its digest. Then: 400 DIGEST_INVALID and no blob appears.
func Test_UploadMonolithic_when_DigestMismatch_then_DigestInvalid(t *testing.T) {
	h, _, _ := uploadTestRouter(t)
	rec := uploadTestBody(t, h, http.MethodPost, "/v2/test/blobs/uploads/?digest="+digestOf([]byte("wanted")), "other", nil)
	requireCode(t, rec, http.StatusBadRequest, "DIGEST_INVALID")
	requireCode(t, serve(t, h, http.MethodGet, "/v2/test/blobs/"+digestOf([]byte("wanted")), nil),
		http.StatusNotFound, "BLOB_UNKNOWN")
}

// Given: a blob present in the registry. When: another repository mounts it.
// Then: 201 and the blob is served under the target repository too.
func Test_UploadMount_when_BlobExists_then_Created(t *testing.T) {
	h, _, store := uploadTestRouter(t)
	digest := store.seed([]byte("shared"))

	rec := serve(t, h, http.MethodPost, "/v2/other/blobs/uploads/?mount="+digest+"&from=test", nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	assertHeader(t, rec.Header(), "Docker-Content-Digest", digest)
	assertHeader(t, rec.Header(), "Location", "/v2/other/blobs/"+digest)
}

// Given: a blob the registry does not hold. When: a mount is attempted.
// Then: 202 and a plain session opens (the prescribed fallback).
func Test_UploadMount_when_BlobMissing_then_SessionFallback(t *testing.T) {
	h, _, _ := uploadTestRouter(t)
	rec := serve(t, h, http.MethodPost, "/v2/other/blobs/uploads/?mount="+digestOf([]byte("absent"))+"&from=test", nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 fallback (body %s)", rec.Code, rec.Body.String())
	}
}

// Given: an open session. When: DELETE cancels it. Then: 204 and the session
// is gone.
func Test_UploadCancel_when_Deleted_then_SessionGone(t *testing.T) {
	h, _, _ := uploadTestRouter(t)
	loc := uploadTestSession(t, h)

	rec := serve(t, h, http.MethodDelete, loc, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("cancel status = %d, want 204", rec.Code)
	}
	requireCode(t, uploadTestBody(t, h, http.MethodPatch, loc, "x", nil),
		http.StatusNotFound, "BLOB_UPLOAD_UNKNOWN")
}

// Given: no session. When: an unknown UUID is addressed. Then: 404
// BLOB_UPLOAD_UNKNOWN.
func Test_UploadSession_when_UnknownUUID_then_UploadUnknown(t *testing.T) {
	h, _, _ := uploadTestRouter(t)
	for _, method := range []string{http.MethodGet, http.MethodPatch, http.MethodPut, http.MethodDelete} {
		requireCode(t, uploadTestBody(t, h, method, "/v2/test/blobs/uploads/deadbeef", "", nil),
			http.StatusNotFound, "BLOB_UPLOAD_UNKNOWN")
	}
}
