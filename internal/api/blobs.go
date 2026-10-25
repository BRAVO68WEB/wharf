package api

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/user/better-docker-registry/internal/db"
	"github.com/user/better-docker-registry/internal/storage"
)

// BlobHandler serves the blob endpoints of the registry API:
// GET, HEAD and DELETE /v2/{name}/blobs/{digest}.
type BlobHandler struct {
	store storage.Storage
	q     db.Querier
}

// NewBlobHandler returns a handler over the blobs held in store. q records
// which repositories reference a blob. Anonymous pulls may read only those.
func NewBlobHandler(store storage.Storage, q db.Querier) *BlobHandler {
	return &BlobHandler{store: store, q: q}
}

// Mount registers the blob routes on r, a /v2 route tree. A repository name
// containing slashes reaches {name} through the collapse NewRouter installs.
func (h *BlobHandler) Mount(r chi.Router) {
	r.Get("/{name}/blobs/{digest}", h.getBlob)
	r.Head("/{name}/blobs/{digest}", h.headBlob)
	r.Delete("/{name}/blobs/{digest}", h.deleteBlob)
}

func (h *BlobHandler) getBlob(w http.ResponseWriter, r *http.Request) {
	h.serveBlob(w, r, true)
}

func (h *BlobHandler) headBlob(w http.ResponseWriter, r *http.Request) {
	h.serveBlob(w, r, false)
}

// serveBlob answers a GET or HEAD for one blob. Bodies are streamed, never
// buffered: a range skips its prefix by discarding bytes and then copies
// exactly the requested length. sendBody is false for HEAD, which answers with
// the headers alone.
func (h *BlobHandler) serveBlob(w http.ResponseWriter, r *http.Request, sendBody bool) {
	digest, ok := blobParams(w, r)
	if !ok {
		return
	}
	if _, ok := h.repoHoldsBlob(w, r, digest); !ok {
		return
	}

	ctx := r.Context()
	size, err := h.store.Size(ctx, digest)
	if err != nil {
		WriteError(w, StorageError(err))
		return
	}

	start, length := int64(0), size
	status := http.StatusOK
	if header := r.Header.Get("Range"); header != "" {
		br, rerr := parseRangeHeader(header, size)
		switch {
		case rerr != nil:
			w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", size))
			WriteError(w, storage.NewError(storage.ErrorCodeRangeInvalid, rerr.Error()))
			return
		case br != nil:
			start, length = br.start, br.length
			status = http.StatusPartialContent
			w.Header().Set("Content-Range",
				fmt.Sprintf("bytes %d-%d/%d", start, start+length-1, size))
		}
	}

	var body io.ReadCloser
	if sendBody {
		body, err = h.store.Get(ctx, digest)
		if err != nil {
			WriteError(w, StorageError(err))
			return
		}
		defer body.Close()
	}

	hdr := w.Header()
	hdr.Set("Docker-Content-Digest", digest)
	hdr.Set("Content-Length", strconv.FormatInt(length, 10))
	hdr.Set("Accept-Ranges", "bytes")
	hdr.Set("ETag", `"`+digest+`"`)
	hdr.Set("Content-Type", "application/octet-stream")
	w.WriteHeader(status)

	if !sendBody {
		return
	}
	if start > 0 {
		if _, err := io.CopyN(io.Discard, body, start); err != nil {
			return // stream broke before the range; the headers are already out
		}
	}
	if _, err := io.CopyN(w, body, length); err != nil {
		return // client vanished mid-stream
	}
}

// repoHoldsBlob reports whether this repository references digest. Every
// read and delete goes through it, so one repository is not a window onto
// blobs it does not use. The bytes stay until garbage collection; delete
// only drops this repository's reference.
func (h *BlobHandler) repoHoldsBlob(w http.ResponseWriter, r *http.Request, digest string) (int64, bool) {
	repo, err := h.q.GetRepositoryByName(r.Context(), RepoName(r))
	if errors.Is(err, sql.ErrNoRows) {
		WriteError(w, storage.NewError(storage.ErrorCodeBlobUnknown, "blob unknown to the registry"))
		return 0, false
	}
	if err != nil {
		WriteError(w, internalFailure())
		return 0, false
	}
	_, err = h.q.GetBlobRef(r.Context(), db.GetBlobRefParams{Digest: digest, RepoID: repo.ID})
	if errors.Is(err, sql.ErrNoRows) {
		WriteError(w, storage.NewError(storage.ErrorCodeBlobUnknown, "blob unknown to the registry"))
		return 0, false
	}
	if err != nil {
		WriteError(w, internalFailure())
		return 0, false
	}
	return repo.ID, true
}

// deleteBlob drops this repository's reference to the blob and answers
// 202 Accepted. The object stays. Another repository may still use that
// digest, and garbage collection removes the bytes only when no reference
// remains.
func (h *BlobHandler) deleteBlob(w http.ResponseWriter, r *http.Request) {
	digest, ok := blobParams(w, r)
	if !ok {
		return
	}
	repoID, ok := h.repoHoldsBlob(w, r, digest)
	if !ok {
		return
	}
	if err := h.q.DeleteBlobRef(r.Context(), db.DeleteBlobRefParams{Digest: digest, RepoID: repoID}); err != nil {
		WriteError(w, internalFailure())
		return
	}
	w.Header().Set("Docker-Content-Digest", digest)
	w.WriteHeader(http.StatusAccepted)
}

// blobParams validates the path parameters of a blob route, answering with the
// registry error contract when either is malformed.
func blobParams(w http.ResponseWriter, r *http.Request) (string, bool) {
	if name := RepoName(r); !ValidName(name) {
		WriteError(w, storage.NewError(storage.ErrorCodeNameInvalid, "invalid repository name"))
		return "", false
	}
	digest := chi.URLParam(r, "digest")
	if !ValidDigest(digest) {
		WriteError(w, storage.NewError(storage.ErrorCodeDigestInvalid, "invalid content digest"))
		return "", false
	}
	return digest, true
}

// byteRange is one satisfiable byte range of a blob of a known size.
type byteRange struct {
	start, length int64
}

// Range parsing failures. A malformed range and a range that does not overlap
// the blob both become 416; they differ only in message.
var (
	errRangeInvalid       = errors.New("invalid byte range")
	errRangeUnsatisfiable = errors.New("requested range not satisfiable")
)

// parseRangeHeader parses a Range header against a representation of size
// bytes and supports one "bytes=" range. It returns (nil, nil) when the header
// must be ignored (a unit other than bytes, or several ranges), (nil, err)
// when the single range is malformed or unsatisfiable, and the range
// otherwise. Semantics match net/http's range parser, which is what
// http.ServeContent serves.
func parseRangeHeader(header string, size int64) (*byteRange, error) {
	const prefix = "bytes="
	if !strings.HasPrefix(header, prefix) {
		return nil, nil // a unit we do not understand is ignored
	}
	spec := strings.TrimSpace(header[len(prefix):])
	if strings.Contains(spec, ",") {
		return nil, nil // multipart ranges are not supported: serve it all
	}

	startSpec, endSpec, found := strings.Cut(spec, "-")
	if !found {
		return nil, errRangeInvalid
	}

	switch {
	case startSpec == "":
		// suffix range: the last n bytes
		n, err := strconv.ParseInt(endSpec, 10, 64)
		if err != nil || n <= 0 {
			return nil, errRangeInvalid
		}
		if n > size {
			n = size
		}
		if n == 0 {
			return nil, errRangeUnsatisfiable // empty blob
		}
		return &byteRange{start: size - n, length: n}, nil

	case endSpec == "":
		start, err := strconv.ParseInt(startSpec, 10, 64)
		if err != nil || start < 0 {
			return nil, errRangeInvalid
		}
		if start >= size {
			return nil, errRangeUnsatisfiable
		}
		return &byteRange{start: start, length: size - start}, nil

	default:
		start, err := strconv.ParseInt(startSpec, 10, 64)
		if err != nil || start < 0 {
			return nil, errRangeInvalid
		}
		end, err := strconv.ParseInt(endSpec, 10, 64)
		if err != nil || end < start {
			return nil, errRangeInvalid
		}
		if start >= size {
			return nil, errRangeUnsatisfiable
		}
		if end >= size {
			end = size - 1 // clamp, the range stays satisfiable
		}
		return &byteRange{start: start, length: end - start + 1}, nil
	}
}
