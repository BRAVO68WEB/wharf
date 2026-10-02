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
	repo, ok := h.resolveRepo(w, r, name)
	if !ok {
		return
	}
	ctx := r.Context()
	if _, err := h.q.GetTag(ctx, db.GetTagParams{RepoID: repo.ID, Name: tag}); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if err := h.q.DeleteTag(ctx, db.DeleteTagParams{RepoID: repo.ID, Name: tag}); err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	audit(h.q, r, auditActionTagDelete, name+":"+tag)
	h.respond(w, r, name)
}

// deleteManifest removes one manifest and every tag pointing at it. Blob
// content stays in storage for GC to reclaim once nothing references it.
func (h *DeleteHandler) deleteManifest(w http.ResponseWriter, r *http.Request) {
	name, digest := RepoPathParam(r), chi.URLParam(r, "digest")
	repo, ok := h.resolveRepo(w, r, name)
	if !ok {
		return
	}
	ctx := r.Context()
	if _, err := h.q.GetManifest(ctx, db.GetManifestParams{RepoID: repo.ID, Digest: digest}); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	// Tags first: tags.manifest_digest references the manifest row.
	if err := h.q.DeleteTagsByManifest(ctx, db.DeleteTagsByManifestParams{
		RepoID:         repo.ID,
		ManifestDigest: digest,
	}); err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if err := h.q.DeleteManifest(ctx, db.DeleteManifestParams{RepoID: repo.ID, Digest: digest}); err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	audit(h.q, r, auditActionManifestDelete, name+"@"+digest)
	h.respond(w, r, name)
}

// resolveRepo looks up the repository named by the request, answering 404 or
// 500 itself when it cannot be resolved.
func (h *DeleteHandler) resolveRepo(w http.ResponseWriter, r *http.Request, name string) (db.Repository, bool) {
	repo, err := h.q.GetRepositoryByName(r.Context(), name)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		http.NotFound(w, r)
		return db.Repository{}, false
	case err != nil:
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return db.Repository{}, false
	}
	return repo, true
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
