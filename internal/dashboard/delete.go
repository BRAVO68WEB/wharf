package dashboard

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/user/better-docker-registry/internal/db"
)

// DeleteHandler serves the dashboard delete flows (plan task 17): unmap a
// tag and remove a manifest with every tag pointing at it. Blob content is
// never touched — garbage collection reclaims whatever ends up unreferenced.
type DeleteHandler struct {
	q db.Querier
}

// NewDeleteHandler returns a handler over the metadata recorded in q.
func NewDeleteHandler(q db.Querier) *DeleteHandler {
	return &DeleteHandler{q: q}
}

// Mount registers the delete routes on r, a subrouter mounted at the repo
// path prefix. Wrap r with CollapseRepoName so multi-segment repository
// names reach the handler intact:
//
//	inner := chi.NewRouter()
//	NewTagsHandler(q).Mount(inner)
//	NewDeleteHandler(q).Mount(inner)
//	root.Mount("/dashboard/repos", CollapseRepoName(inner))
func (h *DeleteHandler) Mount(r chi.Router) {
	r.Delete("/{name}/tags/{tag}", h.deleteTag)
	r.Delete("/{name}/manifests/{digest}", h.deleteManifest)
}

// deleteTag unmaps one tag. The manifest behind it survives — including when
// this was its last tag: a tag delete only removes the name mapping, the same
// as `docker rmi <name>:<tag>`. Removing the manifest itself is the manifest
// route's job.
func (h *DeleteHandler) deleteTag(w http.ResponseWriter, r *http.Request) {
	name, tag := RepoPathParam(r), chi.URLParam(r, "tag")
	if code := h.removeTag(r, name, tag); code != http.StatusNoContent {
		writeStatus(w, r, code)
		return
	}
	h.respond(w, r, name)
}

// removeTag unmaps one tag and records the audit entry. It does not write a
// response: the HTML handler redirects and the JSON handler returns 204.
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

// deleteManifest removes one manifest and every tag pointing at it. Blob
// content stays in storage for GC to reclaim once nothing references it.
func (h *DeleteHandler) deleteManifest(w http.ResponseWriter, r *http.Request) {
	name, digest := RepoPathParam(r), chi.URLParam(r, "digest")
	if code := h.removeManifest(r, name, digest); code != http.StatusNoContent {
		writeStatus(w, r, code)
		return
	}
	h.respond(w, r, name)
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

// writeStatus is the HTML delete handler's answer for a remove that did not
// succeed. The JSON API maps the same codes itself.
func writeStatus(w http.ResponseWriter, r *http.Request, code int) {
	if code == http.StatusNotFound {
		http.NotFound(w, r)
		return
	}
	http.Error(w, "internal server error", code)
}

// respond answers a completed delete. HTMX callers get an empty 200 — the
// "no content means remove it" signal behind hx-swap="outerHTML" — while
// plain callers are redirected to the repository's tag list with 303.
func (h *DeleteHandler) respond(w http.ResponseWriter, r *http.Request, name string) {
	if isHTMX(r) {
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/dashboard/repos/"+name, http.StatusSeeOther)
}
