package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/user/better-docker-registry/internal/db"
	"github.com/user/better-docker-registry/internal/storage"
)

// sessionTTL bounds how long an idle upload session stays resumable. Expired
// sessions are reaped lazily on access.
const sessionTTL = 24 * time.Hour

// UploadHandler serves the blob upload protocol: POST opens a session (or
// pushes a whole blob at once, or cross-mounts one from another repository),
// GET reports progress, PATCH appends a chunk, PUT commits, DELETE cancels.
// Chunk bodies spool to a temp file so a multi-GB push never lives in memory;
// the commit streams that file through storage.Put, which re-verifies the
// digest before the blob becomes readable.
type UploadHandler struct {
	store   storage.Storage
	q       db.Querier
	maxSize int64
}

// NewUploadHandler returns the upload endpoints over blobs held in store and
// session state recorded in q. maxSize caps one upload in bytes; zero is
// unlimited.
func NewUploadHandler(store storage.Storage, q db.Querier, maxSize int64) *UploadHandler {
	return &UploadHandler{store: store, q: q, maxSize: maxSize}
}

// Mount registers the upload routes on r, a /v2 route tree.
func (h *UploadHandler) Mount(r chi.Router) {
	r.Post("/{name}/blobs/uploads/", h.startUpload)
	r.Post("/{name}/blobs/uploads", h.startUpload)
	r.Get("/{name}/blobs/uploads/{uuid}", h.statusUpload)
	r.Patch("/{name}/blobs/uploads/{uuid}", h.patchUpload)
	r.Put("/{name}/blobs/uploads/{uuid}", h.putUpload)
	r.Delete("/{name}/blobs/uploads/{uuid}", h.cancelUpload)
}

// startUpload dispatches the three POST shapes of the protocol:
//
//	?mount=<digest>[&from=<repo>]  cross-repository mount
//	?digest=<digest> + body       monolithic push in one request
//	plain                         open a chunked upload session
//
// A mount of a blob the registry does not hold falls back to opening a
// session, which is what the spec prescribes.
func (h *UploadHandler) startUpload(w http.ResponseWriter, r *http.Request) {
	name, ok := uploadName(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	if mount := q.Get("mount"); mount != "" {
		if !ValidDigest(mount) {
			WriteError(w, storage.NewError(storage.ErrorCodeDigestInvalid, "invalid content digest"))
			return
		}
		found, err := h.store.Exists(r.Context(), mount)
		if err != nil {
			WriteError(w, StorageError(err))
			return
		}
		if found {
			h.mountBlob(w, r, name, mount)
			return
		}
	}
	if digest := q.Get("digest"); digest != "" {
		h.monolithic(w, r, name, digest)
		return
	}
	h.openSession(w, r, name)
}

// mountBlob records a cross-repository mount: the target repository gains a
// reference to the existing blob with no bytes moved.
func (h *UploadHandler) mountBlob(w http.ResponseWriter, r *http.Request, name, digest string) {
	repo, err := h.q.CreateRepository(r.Context(), name)
	if err != nil {
		WriteError(w, internalFailure())
		return
	}
	if err := h.q.AddBlobRef(r.Context(), db.AddBlobRefParams{Digest: digest, RepoID: repo.ID}); err != nil {
		WriteError(w, internalFailure())
		return
	}
	writeBlobCreated(w, name, digest)
}

// monolithic accepts a whole blob in the POST body: the stream goes straight
// to storage, whose Put verifies the digest and leaves nothing readable on
// mismatch.
func (h *UploadHandler) monolithic(w http.ResponseWriter, r *http.Request, name, digest string) {
	if !ValidDigest(digest) {
		WriteError(w, storage.NewError(storage.ErrorCodeDigestInvalid, "invalid content digest"))
		return
	}
	body := h.boundedBody(w, r)
	if body == nil {
		return
	}
	n, err := h.store.Put(r.Context(), digest, body)
	if err != nil {
		if errors.Is(err, storage.ErrBlobInvalid) {
			WriteError(w, storage.NewError(storage.ErrorCodeDigestInvalid,
				fmt.Sprintf("provided digest did not match the uploaded content (%d bytes received)", n)))
			return
		}
		WriteError(w, StorageError(err))
		return
	}
	repo, err := h.q.CreateRepository(r.Context(), name)
	if err != nil {
		WriteError(w, internalFailure())
		return
	}
	if err := h.q.AddBlobRef(r.Context(), db.AddBlobRefParams{Digest: digest, RepoID: repo.ID}); err != nil {
		WriteError(w, internalFailure())
		return
	}
	writeBlobCreated(w, name, digest)
}

// openSession starts a chunked upload: an empty spool file plus a session
// row. The202 Location is the session URL every later chunk request targets.
func (h *UploadHandler) openSession(w http.ResponseWriter, r *http.Request, name string) {
	repo, err := h.q.CreateRepository(r.Context(), name)
	if err != nil {
		WriteError(w, internalFailure())
		return
	}
	tmp, err := os.CreateTemp("", "bdr-upload-*")
	if err != nil {
		WriteError(w, internalFailure())
		return
	}
	uuid, err := newUploadUUID()
	if err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		WriteError(w, internalFailure())
		return
	}
	now := time.Now()
	err = h.q.CreateUpload(r.Context(), db.CreateUploadParams{
		Uuid:      uuid,
		RepoID:    repo.ID,
		Digest:    "",
		Offset:    0,
		Size:      0,
		TempPath:  tmp.Name(),
		CreatedAt: now.Unix(),
		ExpiresAt: now.Add(sessionTTL).Unix(),
	})
	_ = tmp.Close()
	if err != nil {
		_ = os.Remove(tmp.Name())
		WriteError(w, internalFailure())
		return
	}
	writeSession(w, name, uuid, 0, http.StatusAccepted)
}

// writeBlobCreated answers 201 for a blob that now exists in the repository.
func writeBlobCreated(w http.ResponseWriter, name, digest string) {
	hdr := w.Header()
	hdr.Set("Docker-Content-Digest", digest)
	hdr.Set("Location", "/v2/"+name+"/blobs/"+digest)
	hdr.Set("Content-Length", "0")
	w.WriteHeader(http.StatusCreated)
}

// writeSession answers an upload-session response: the session Location,
// the upload UUID and the received-byte range the client may resume from.
func writeSession(w http.ResponseWriter, name, uuid string, offset int64, status int) {
	last := offset - 1
	if last < 0 {
		last = 0
	}
	hdr := w.Header()
	hdr.Set("Location", "/v2/"+name+"/blobs/uploads/"+uuid)
	hdr.Set("Docker-Upload-UUID", uuid)
	hdr.Set("Range", fmt.Sprintf("0-%d", last))
	hdr.Set("Content-Length", "0")
	w.WriteHeader(status)
}

// boundedBody returns the request body under the configured size limit, or
// answers SIZE_INVALID and returns nil when the limit cannot be applied.
func (h *UploadHandler) boundedBody(w http.ResponseWriter, r *http.Request) io.ReadCloser {
	if h.maxSize <= 0 {
		return r.Body
	}
	return http.MaxBytesReader(w, r.Body, h.maxSize)
}

func newUploadUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
