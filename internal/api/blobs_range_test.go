package api_test

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/BRAVO68WEB/wharf/internal/api"
	"github.com/BRAVO68WEB/wharf/internal/storage"
)

// rangeFixture serves a 1000-byte blob whose bytes spell "0123456789" ten
// times, so any wrong slice is visible in the body.
func rangeFixture(t *testing.T) (*fakeStorage, http.Handler, string, []byte) {
	t.Helper()
	store := newFakeStorage()
	data := bytes.Repeat([]byte("0123456789"), 100)
	digest := store.seed(data)
	q := newTestQueries(t)
	seedBlobRef(t, q, "test", digest)
	return store, api.NewRouter(store, q), digest, data
}

func Test_GetBlob_when_RangeStartEnd_then_206PartialBytes(t *testing.T) {
	// Given: a 1000-byte blob
	_, router, digest, data := rangeFixture(t)

	// When: the first 100 bytes are requested
	rec := serve(t, router, http.MethodGet, "/v2/test/blobs/"+digest,
		map[string]string{"Range": "bytes=0-99"})

	// Then: 206 with exactly those bytes
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusPartialContent, rec.Body.String())
	}
	if !bytes.Equal(rec.Body.Bytes(), data[0:100]) {
		t.Fatalf("body = %d bytes, want data[0:100]", rec.Body.Len())
	}
	assertHeader(t, rec.Header(), "Content-Range", "bytes 0-99/1000")
	assertHeader(t, rec.Header(), "Content-Length", "100")
	assertHeader(t, rec.Header(), "Docker-Content-Digest", digest)
	assertHeader(t, rec.Header(), "Accept-Ranges", "bytes")
}

func Test_GetBlob_when_RangeOpenEnded_then_206FromStart(t *testing.T) {
	// Given: a 1000-byte blob
	_, router, digest, data := rangeFixture(t)

	// When: everything from byte 900 is requested
	rec := serve(t, router, http.MethodGet, "/v2/test/blobs/"+digest,
		map[string]string{"Range": "bytes=900-"})

	// Then: 206 with the tail of the blob
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusPartialContent)
	}
	if !bytes.Equal(rec.Body.Bytes(), data[900:]) {
		t.Fatalf("body = %d bytes, want data[900:]", rec.Body.Len())
	}
	assertHeader(t, rec.Header(), "Content-Range", "bytes 900-999/1000")
	assertHeader(t, rec.Header(), "Content-Length", "100")
}

func Test_GetBlob_when_RangeSuffix_then_206LastBytes(t *testing.T) {
	// Given: a 1000-byte blob
	_, router, digest, data := rangeFixture(t)

	// When: the last 100 bytes are requested by suffix length
	rec := serve(t, router, http.MethodGet, "/v2/test/blobs/"+digest,
		map[string]string{"Range": "bytes=-100"})

	// Then: 206 with the last 100 bytes
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusPartialContent)
	}
	if !bytes.Equal(rec.Body.Bytes(), data[900:]) {
		t.Fatalf("body = %d bytes, want data[900:]", rec.Body.Len())
	}
	assertHeader(t, rec.Header(), "Content-Range", "bytes 900-999/1000")
}

func Test_GetBlob_when_RangeEndPastSize_then_ClampedToSize(t *testing.T) {
	// Given: a 1000-byte blob
	_, router, digest, data := rangeFixture(t)

	// When: a range runs past the end of the blob
	rec := serve(t, router, http.MethodGet, "/v2/test/blobs/"+digest,
		map[string]string{"Range": "bytes=0-5000"})

	// Then: 206 with the clamped remainder
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusPartialContent)
	}
	if !bytes.Equal(rec.Body.Bytes(), data) {
		t.Fatalf("body = %d bytes, want all 1000", rec.Body.Len())
	}
	assertHeader(t, rec.Header(), "Content-Range", "bytes 0-999/1000")
	assertHeader(t, rec.Header(), "Content-Length", "1000")
}

func Test_GetBlob_when_RangeStartPastSize_then_416RangeInvalid(t *testing.T) {
	// Given: a 1000-byte blob
	_, router, digest, _ := rangeFixture(t)

	// When: a range starts beyond the end of the blob
	rec := serve(t, router, http.MethodGet, "/v2/test/blobs/"+digest,
		map[string]string{"Range": "bytes=2000-3000"})

	// Then: 416 with the entity length and RANGE_INVALID
	if rec.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusRequestedRangeNotSatisfiable, rec.Body.String())
	}
	assertHeader(t, rec.Header(), "Content-Range", "bytes */1000")
	if code := decodeErrors(t, rec).Errors[0].Code; code != storage.ErrorCodeRangeInvalid {
		t.Fatalf("error code = %s, want %s", code, storage.ErrorCodeRangeInvalid)
	}
}

func Test_GetBlob_when_RangeMalformed_then_416RangeInvalid(t *testing.T) {
	// Given: a 1000-byte blob
	_, router, digest, _ := rangeFixture(t)

	// When: the Range header is not parseable as a byte range
	rec := serve(t, router, http.MethodGet, "/v2/test/blobs/"+digest,
		map[string]string{"Range": "bytes=abc"})

	// Then: 416 RANGE_INVALID
	if rec.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusRequestedRangeNotSatisfiable, rec.Body.String())
	}
	if code := decodeErrors(t, rec).Errors[0].Code; code != storage.ErrorCodeRangeInvalid {
		t.Fatalf("error code = %s, want %s", code, storage.ErrorCodeRangeInvalid)
	}
}

func Test_GetBlob_when_RangeUnitUnknown_then_200FullBody(t *testing.T) {
	// Given: a 1000-byte blob
	_, router, digest, data := rangeFixture(t)

	// When: the Range header uses a unit this registry does not understand
	rec := serve(t, router, http.MethodGet, "/v2/test/blobs/"+digest,
		map[string]string{"Range": "items=0-10"})

	// Then: the header is ignored and the whole blob is served
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !bytes.Equal(rec.Body.Bytes(), data) {
		t.Fatalf("body = %d bytes, want all 1000", rec.Body.Len())
	}
}

func Test_GetBlob_when_MultipleRanges_then_200FullBody(t *testing.T) {
	// Given: a 1000-byte blob
	_, router, digest, data := rangeFixture(t)

	// When: more than one range is requested (multipart is not supported)
	rec := serve(t, router, http.MethodGet, "/v2/test/blobs/"+digest,
		map[string]string{"Range": "bytes=0-9,20-29"})

	// Then: the header is ignored and the whole blob is served
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !bytes.Equal(rec.Body.Bytes(), data) {
		t.Fatalf("body = %d bytes, want all 1000", rec.Body.Len())
	}
}

func Test_HeadBlob_when_RangePresent_then_206HeadersOnly(t *testing.T) {
	// Given: a 1000-byte blob
	_, router, digest, _ := rangeFixture(t)

	// When: a ranged HEAD is issued
	rec := serve(t, router, http.MethodHead, "/v2/test/blobs/"+digest,
		map[string]string{"Range": "bytes=0-99"})

	// Then: the headers describe the range and no body is written
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusPartialContent)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("body = %q, want empty", rec.Body.String())
	}
	assertHeader(t, rec.Header(), "Content-Range", "bytes 0-99/1000")
	assertHeader(t, rec.Header(), "Content-Length", "100")
}
