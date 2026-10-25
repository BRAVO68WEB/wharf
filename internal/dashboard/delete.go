package dashboard

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/user/better-docker-registry/internal/db"
)

// DeleteHandler removes tags and manifests for the JSON API. Blob bytes stay
// in storage; garbage collection reclaims whatever ends up unreferenced.
type DeleteHandler struct {
	q db.Querier
}

// NewDeleteHandler returns a handler over the metadata recorded in q.
func NewDeleteHandler(q db.Querier) *DeleteHandler {
	return &DeleteHandler{q: q}
}

// removeTag unmaps one tag and records the audit entry. It does not write a
// response: the JSON handler returns 204.
func (h *DeleteHandler) removeTag(r *http.Request, name, tag string) int {
	repo, code := h.lookupRepo(r, name)
	if code != 0 {
		return code
	}
	ctx := r.Context()
	if _, err := h.q.GetTag(ctx, db.GetTagParams{RepoID: repo.ID, Name: tag}); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return http.StatusNotFound
		}
		return http.StatusInternalServerError
	}
	if err := h.q.DeleteTag(ctx, db.DeleteTagParams{RepoID: repo.ID, Name: tag}); err != nil {
		return http.StatusInternalServerError
	}
	audit(h.q, r, auditActionTagDelete, name+":"+tag)
	return http.StatusNoContent
}

// removeManifest deletes one manifest and every tag pointing at it. Blob
// bytes stay for garbage collection. Like removeTag, it leaves the response
// to the caller.
func (h *DeleteHandler) removeManifest(r *http.Request, name, digest string) int {
	repo, code := h.lookupRepo(r, name)
	if code != 0 {
		return code
	}
	ctx := r.Context()
	if _, err := h.q.GetManifest(ctx, db.GetManifestParams{RepoID: repo.ID, Digest: digest}); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return http.StatusNotFound
		}
		return http.StatusInternalServerError
	}
	// Tags first: tags.manifest_digest references the manifest row.
	if err := h.q.DeleteTagsByManifest(ctx, db.DeleteTagsByManifestParams{
		RepoID:         repo.ID,
		ManifestDigest: digest,
	}); err != nil {
		return http.StatusInternalServerError
	}
	if err := h.q.DeleteManifest(ctx, db.DeleteManifestParams{RepoID: repo.ID, Digest: digest}); err != nil {
		return http.StatusInternalServerError
	}
	audit(h.q, r, auditActionManifestDelete, name+"@"+digest)
	return http.StatusNoContent
}

// lookupRepo resolves a repository without writing a response. code is 0 when
// the repository exists.
func (h *DeleteHandler) lookupRepo(r *http.Request, name string) (db.Repository, int) {
	repo, err := h.q.GetRepositoryByName(r.Context(), name)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return db.Repository{}, http.StatusNotFound
	case err != nil:
		return db.Repository{}, http.StatusInternalServerError
	}
	return repo, 0
}
