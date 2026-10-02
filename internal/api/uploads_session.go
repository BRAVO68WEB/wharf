package api

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/user/better-docker-registry/internal/db"
	"github.com/user/better-docker-registry/internal/storage"
)

// statusUpload answers 204 with the session Location and the received range,
// the resume probe the chunked protocol allows.
func (h *UploadHandler) statusUpload(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := h.loadSession(w, r)
	if !ok {
		return
	}
	writeSession(w, RepoName(r), sess.Uuid, sess.Offset, http.StatusNoContent)
}

// patchUpload appends one chunk to the spool file. With a Content-Range the
// chunk must continue exactly at the current end; anything else is refused
// with 416 and the session stays resumable. Without one the chunk simply
// appends.
func (h *UploadHandler) patchUpload(w http.ResponseWriter, r *http.Request) {
	h.appendChunk(w, r, false)
}

// putUpload appends the final chunk (if any) and commits the blob. The digest
// query parameter is the content address the spool file must hash to.
func (h *UploadHandler) putUpload(w http.ResponseWriter, r *http.Request) {
	h.appendChunk(w, r, true)
}

// cancelUpload drops the session and its spool file.
func (h *UploadHandler) cancelUpload(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := h.loadSession(w, r)
	if !ok {
		return
	}
	h.discardSession(r, sess)
	w.WriteHeader(http.StatusNoContent)
}

// appendChunk streams the request body onto the session spool file and, when
// commit is set, moves the result into blob storage under the ?digest=
// content address.
func (h *UploadHandler) appendChunk(w http.ResponseWriter, r *http.Request, commit bool) {
	sess, name, ok := h.loadSession(w, r)
	if !ok {
		return
	}

	digest := ""
	if commit {
		digest = r.URL.Query().Get("digest")
		if !ValidDigest(digest) {
			WriteError(w, storage.NewError(storage.ErrorCodeDigestInvalid,
				"the digest parameter is required and must be well-formed"))
			return
		}
	}

	body := h.boundedBody(w, r)
	if body == nil {
		return
	}
	offset, err := h.appendToSpool(r, sess, body)
	if err != nil {
		var errRange *errChunkRange
		switch {
		case errors.As(err, &errRange):
			writeSession(w, name, sess.Uuid, sess.Offset, http.StatusRequestedRangeNotSatisfiable)
		case errors.Is(err, errUploadGone):
			WriteError(w, storage.NewError(storage.ErrorCodeBlobUploadUnknown, "blob upload unknown to the registry"))
		default:
			WriteError(w, internalFailure())
		}
		return
	}
	if err := h.q.UpdateUploadProgress(r.Context(), db.UpdateUploadProgressParams{
		Uuid:   sess.Uuid,
		Digest: digest,
		Offset: offset,
		Size:   offset,
	}); err != nil {
		WriteError(w, internalFailure())
		return
	}

	if !commit {
		writeSession(w, name, sess.Uuid, offset, http.StatusAccepted)
		return
	}
	h.commit(w, r, name, sess, digest)
}

// commit streams the spool file into blob storage. storage.Put re-hashes and
// verifies the content, so a digest mismatch destroys the session without a
// readable blob appearing anywhere.
func (h *UploadHandler) commit(w http.ResponseWriter, r *http.Request, name string, sess db.Upload, digest string) {
	f, err := os.Open(sess.TempPath)
	if err != nil {
		WriteError(w, internalFailure())
		return
	}
	defer f.Close()

	if _, err := h.store.Put(r.Context(), digest, f); err != nil {
		h.discardSession(r, sess)
		if errors.Is(err, storage.ErrBlobInvalid) {
			WriteError(w, storage.NewError(storage.ErrorCodeDigestInvalid,
				"provided digest did not match the uploaded content"))
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
	h.discardSession(r, sess)
	writeBlobCreated(w, name, digest)
}

// appendToSpool writes one chunk at the session's current end. A Content-Range
// that does not start exactly there leaves the session untouched and fails
// with errChunkRange.
func (h *UploadHandler) appendToSpool(r *http.Request, sess db.Upload, body io.Reader) (int64, error) {
	if raw := r.Header.Get("Content-Range"); raw != "" {
		start, ok := parseContentRangeStart(raw)
		if !ok || start != sess.Offset {
			return 0, &errChunkRange{want: sess.Offset}
		}
	}
	f, err := os.OpenFile(sess.TempPath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return 0, errUploadGone
	}
	defer f.Close()
	n, err := io.Copy(f, body)
	if err != nil {
		return 0, err
	}
	return sess.Offset + n, nil
}

// loadSession resolves the addressed upload session against its repository
// and reaps it once expired. ok is false after the error response is written.
func (h *UploadHandler) loadSession(w http.ResponseWriter, r *http.Request) (db.Upload, string, bool) {
	name, ok := uploadName(w, r)
	if !ok {
		return db.Upload{}, "", false
	}
	uuid := chi.URLParam(r, "uuid")
	sess, err := h.q.GetUpload(r.Context(), uuid)
	if errors.Is(err, sql.ErrNoRows) {
		WriteError(w, storage.NewError(storage.ErrorCodeBlobUploadUnknown, "blob upload unknown to the registry"))
		return db.Upload{}, "", false
	}
	if err != nil {
		WriteError(w, internalFailure())
		return db.Upload{}, "", false
	}
	repo, err := h.q.GetRepositoryByName(r.Context(), name)
	if err != nil || repo.ID != sess.RepoID || sess.ExpiresAt <= time.Now().Unix() {
		h.discardSession(r, sess)
		WriteError(w, storage.NewError(storage.ErrorCodeBlobUploadUnknown, "blob upload unknown to the registry"))
		return db.Upload{}, "", false
	}
	return sess, name, true
}

// discardSession drops the session row and spool file. Best effort: both are
// garbage even if one removal fails.
func (h *UploadHandler) discardSession(r *http.Request, sess db.Upload) {
	if err := h.q.DeleteUpload(r.Context(), sess.Uuid); err != nil && !errors.Is(err, sql.ErrNoRows) {
		slog.Warn("registry: upload session delete failed", "uuid", sess.Uuid, "error", err)
	}
	if sess.TempPath != "" {
		if err := os.Remove(sess.TempPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("registry: upload spool cleanup failed", "uuid", sess.Uuid, "error", err)
		}
	}
}

// uploadName validates the repository name of an upload route.
func uploadName(w http.ResponseWriter, r *http.Request) (string, bool) {
	name := RepoName(r)
	if !ValidName(name) {
		WriteError(w, storage.NewError(storage.ErrorCodeNameInvalid, "invalid repository name"))
		return "", false
	}
	return name, true
}

// errChunkRange reports a chunk that does not continue at the session end.
type errChunkRange struct{ want int64 }

func (e *errChunkRange) Error() string {
	return fmt.Sprintf("chunk does not continue at offset %d", e.want)
}

var errUploadGone = errors.New("upload spool file gone")

// parseContentRangeStart reads the start byte of a "0-99", "bytes=0-99" or
// "bytes 0-99/100" range header. The chunked protocol uses the plain form.
func parseContentRangeStart(raw string) (int64, bool) {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "bytes=")
	s = strings.TrimPrefix(s, "bytes ")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	startSpec, endSpec, found := strings.Cut(strings.TrimSpace(s), "-")
	if !found {
		return 0, false
	}
	start, err := strconv.ParseInt(startSpec, 10, 64)
	if err != nil || start < 0 {
		return 0, false
	}
	if endSpec != "" {
		if _, err := strconv.ParseInt(endSpec, 10, 64); err != nil {
			return 0, false
		}
	}
	return start, true
}
