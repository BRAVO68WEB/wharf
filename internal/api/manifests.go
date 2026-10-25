package api

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BRAVO68WEB/wharf/internal/db"
	"github.com/BRAVO68WEB/wharf/internal/storage"
)

// ManifestHandler serves the manifest endpoints of the registry API:
// GET, HEAD, PUT and DELETE /v2/{name}/manifests/{reference}. Manifests are
// metadata; their payloads live in SQLite and are served from there, so the
// handler needs the metadata queries but no blob storage.
type ManifestHandler struct {
	q db.Querier
}

// NewManifestHandler returns a handler over the manifests recorded in q.
func NewManifestHandler(q db.Querier) *ManifestHandler {
	return &ManifestHandler{q: q}
}

// Mount registers the manifest routes on r, a /v2 route tree. A repository
// name containing slashes reaches {name} through the collapse NewRouter
// installs.
func (h *ManifestHandler) Mount(r chi.Router) {
	r.Get("/{name}/manifests/{reference}", h.getManifest)
	r.Head("/{name}/manifests/{reference}", h.headManifest)
	r.Put("/{name}/manifests/{reference}", h.putManifest)
	r.Delete("/{name}/manifests/{reference}", h.deleteManifest)
}

func (h *ManifestHandler) getManifest(w http.ResponseWriter, r *http.Request) {
	h.serveManifest(w, r, true)
}

func (h *ManifestHandler) headManifest(w http.ResponseWriter, r *http.Request) {
	h.serveManifest(w, r, false)
}

// serveManifest answers a GET or HEAD for one manifest, by tag or by digest.
// sendBody is false for HEAD, which answers with the headers alone.
func (h *ManifestHandler) serveManifest(w http.ResponseWriter, r *http.Request, sendBody bool) {
	target, ok := readManifestTarget(w, r)
	if !ok {
		return
	}

	m, found, err := h.lookup(r.Context(), target)
	if err != nil {
		WriteError(w, internalFailure())
		return
	}
	if !found || !acceptsManifest(strings.Join(r.Header.Values("Accept"), ", "), m.MediaType) {
		// A manifest the client cannot accept is indistinguishable from one
		// that does not exist: there is no "wrong representation" code, and
		// telling a puller "found it, wrong type" breaks its retry loop.
		WriteError(w, storage.NewError(storage.ErrorCodeManifestUnknown, "manifest unknown to the registry"))
		return
	}

	hdr := w.Header()
	hdr.Set("Docker-Content-Digest", m.Digest)
	hdr.Set("Content-Type", m.MediaType)
	hdr.Set("Content-Length", strconv.FormatInt(m.Size, 10))
	hdr.Set("ETag", `"`+m.Digest+`"`)
	w.WriteHeader(http.StatusOK)
	if sendBody {
		_, _ = w.Write(m.Payload)
	}
}

// putManifest stores a manifest and, when the reference is a tag, points the
// tag at it. The payload is validated before anything is written and every
// write is an upsert, so a retried PUT lands in the same state.
func (h *ManifestHandler) putManifest(w http.ResponseWriter, r *http.Request) {
	target, ok := readManifestTarget(w, r)
	if !ok {
		return
	}

	payload, err := io.ReadAll(io.LimitReader(r.Body, maxManifestSize+1))
	if err != nil {
		WriteError(w, invalidManifest("manifest payload could not be read"))
		return
	}
	if int64(len(payload)) > maxManifestSize {
		WriteError(w, storage.NewError(storage.ErrorCodeSizeInvalid, "manifest payload is too large"))
		return
	}

	algo := "sha256"
	if target.byDigest {
		algo = DigestAlgo(target.ref)
	}
	info, verr := parseManifest(r.Header.Get("Content-Type"), algo, payload)
	if verr != nil {
		WriteError(w, verr)
		return
	}
	if target.byDigest && target.ref != info.digest {
		WriteError(w, storage.NewError(storage.ErrorCodeDigestInvalid,
			"provided digest did not match the manifest content"))
		return
	}

	ctx := r.Context()
	repo, err := h.q.CreateRepository(ctx, target.name)
	if err != nil {
		WriteError(w, internalFailure())
		return
	}
	if err := h.q.UpsertManifest(ctx, db.UpsertManifestParams{
		RepoID:    repo.ID,
		Digest:    info.digest,
		MediaType: info.mediaType,
		Payload:   payload,
		Size:      int64(len(payload)),
	}); err != nil {
		WriteError(w, internalFailure())
		return
	}
	for _, referenced := range info.references {
		if err := h.q.AddBlobRef(ctx, db.AddBlobRefParams{Digest: referenced, RepoID: repo.ID}); err != nil {
			WriteError(w, internalFailure())
			return
		}
	}
	if !target.byDigest {
		if err := h.q.SetTag(ctx, db.SetTagParams{
			RepoID:         repo.ID,
			Name:           target.ref,
			ManifestDigest: info.digest,
			UpdatedAt:      time.Now().Unix(),
		}); err != nil {
			WriteError(w, internalFailure())
			return
		}
	}

	hdr := w.Header()
	hdr.Set("Docker-Content-Digest", info.digest)
	hdr.Set("Location", "/v2/"+target.name+"/manifests/"+info.digest)
	if info.subject != "" {
		hdr.Set("OCI-Subject", info.subject)
	}
	w.WriteHeader(http.StatusCreated)
}

// deleteManifest removes a tag mapping or a manifest, depending on the
// reference. A tag delete unmaps the name and leaves the manifest (and every
// other tag) alone — `docker rmi name:tag` semantics. A digest delete removes
// the manifest and every tag pointing at it. Referenced blobs are left to the
// GC.
func (h *ManifestHandler) deleteManifest(w http.ResponseWriter, r *http.Request) {
	target, ok := readManifestTarget(w, r)
	if !ok {
		return
	}

	m, found, err := h.lookup(r.Context(), target)
	if err != nil {
		WriteError(w, internalFailure())
		return
	}
	if !found {
		WriteError(w, storage.NewError(storage.ErrorCodeManifestUnknown, "manifest unknown to the registry"))
		return
	}

	if !target.byDigest {
		if err := h.q.DeleteTag(r.Context(), db.DeleteTagParams{
			RepoID: m.RepoID,
			Name:   target.ref,
		}); err != nil {
			WriteError(w, internalFailure())
			return
		}
		w.Header().Set("Docker-Content-Digest", m.Digest)
		w.WriteHeader(http.StatusAccepted)
		return
	}

	if err := h.q.DeleteTagsByManifest(r.Context(), db.DeleteTagsByManifestParams{
		RepoID:         m.RepoID,
		ManifestDigest: m.Digest,
	}); err != nil {
		WriteError(w, internalFailure())
		return
	}
	if err := h.q.DeleteManifest(r.Context(), db.DeleteManifestParams{
		RepoID: m.RepoID,
		Digest: m.Digest,
	}); err != nil {
		WriteError(w, internalFailure())
		return
	}

	hdr := w.Header()
	hdr.Set("Docker-Content-Digest", m.Digest)
	w.WriteHeader(http.StatusAccepted)
}

// lookup resolves a manifest reference inside its repository. found is false
// when the repository, tag or manifest does not exist; that is a normal
// outcome, not an error.
func (h *ManifestHandler) lookup(ctx context.Context, target manifestTarget) (db.Manifest, bool, error) {
	repo, err := h.q.GetRepositoryByName(ctx, target.name)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Manifest{}, false, nil
	}
	if err != nil {
		return db.Manifest{}, false, err
	}

	var m db.Manifest
	if target.byDigest {
		m, err = h.q.GetManifest(ctx, db.GetManifestParams{RepoID: repo.ID, Digest: target.ref})
	} else {
		m, err = h.q.GetManifestByTag(ctx, db.GetManifestByTagParams{RepoID: repo.ID, Name: target.ref})
	}
	if errors.Is(err, sql.ErrNoRows) {
		return db.Manifest{}, false, nil
	}
	if err != nil {
		return db.Manifest{}, false, err
	}
	return m, true, nil
}

// manifestTarget is a validated manifest address: a repository name plus a
// reference that is a digest or a tag.
type manifestTarget struct {
	name     string
	ref      string
	byDigest bool
}

// readManifestTarget validates the path parameters of a manifest route and
// answers with the registry error contract when either is malformed.
func readManifestTarget(w http.ResponseWriter, r *http.Request) (manifestTarget, bool) {
	name := RepoName(r)
	if !ValidName(name) {
		WriteError(w, storage.NewError(storage.ErrorCodeNameInvalid, "invalid repository name"))
		return manifestTarget{}, false
	}

	ref := chi.URLParam(r, "reference")
	switch {
	case ValidDigest(ref):
		return manifestTarget{name: name, ref: ref, byDigest: true}, true
	case ValidTag(ref):
		return manifestTarget{name: name, ref: ref}, true
	case strings.Contains(ref, ":"):
		WriteError(w, storage.NewError(storage.ErrorCodeDigestInvalid, "invalid content digest"))
	default:
		WriteError(w, storage.NewError(storage.ErrorCodeTagInvalid, "invalid tag"))
	}
	return manifestTarget{}, false
}

// acceptsManifest reports whether an Accept header admits a manifest of the
// given media type. A missing header means no preference and admits
// everything (RFC 9110 §12.5.1); an empty entry list is the same. Quality
// values are ignored: offering a type accepts receiving it.
func acceptsManifest(accept, mediaType string) bool {
	if strings.TrimSpace(accept) == "" {
		return true
	}
	for _, entry := range strings.Split(accept, ",") {
		offered, _, err := mime.ParseMediaType(entry)
		if err != nil {
			continue
		}
		switch {
		case offered == "*/*", offered == mediaType:
			return true
		case strings.HasSuffix(offered, "/*"):
			if strings.HasPrefix(mediaType, strings.TrimSuffix(offered, "*")) {
				return true
			}
		}
	}
	return false
}

// internalFailure is the wire error of a metadata store failure. The message
// is fixed: a database error can carry SQL and paths the client has no
// business seeing.
func internalFailure() *storage.Error {
	return storage.NewError(errorCodeInternal, "internal registry error")
}
