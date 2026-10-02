package dashboard

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/user/better-docker-registry/internal/api"
	"github.com/user/better-docker-registry/internal/db"
)

// maxRetagFormBytes bounds the retag form body. Two short text fields do not
// need more, and ParseForm must not read unbounded input (the login flow
// caps its form the same way).
const maxRetagFormBytes = 8 << 10

// RetagHandler serves the dashboard retag flow (plan task 21): copy the
// manifest behind one tag to a new tag name — in this repository or another —
// without touching the original tag. One source tag per request; there is no
// bulk retag.
type RetagHandler struct {
	q db.Querier
}

// NewRetagHandler returns a handler over the metadata recorded in q.
func NewRetagHandler(q db.Querier) *RetagHandler {
	return &RetagHandler{q: q}
}

// Mount registers the retag routes on r, a subrouter mounted at the repo
// path prefix. Wrap r with CollapseRepoName so multi-segment repository
// names reach the handler intact (same mount as the delete flows):
//
//	inner := chi.NewRouter()
//	NewTagsHandler(q).Mount(inner)
//	NewRetagHandler(q).Mount(inner)
//	root.Mount("/dashboard/repos", CollapseRepoName(inner))
//
// GET renders the retag form for one tag; POST performs the copy.
func (h *RetagHandler) Mount(r chi.Router) {
	r.Get("/{name}/tags/{tag}/retag", h.retagForm)
	r.Post("/{name}/tags/{tag}/retag", h.retag)
}

// retagFormData is the view model of the retag form: the source reference,
// where the new tag goes, the CSRF token its hx-post carries, and the
// validation message to show when the form must be fixed.
type retagFormData struct {
	RepoName   string
	TagName    string
	TargetRepo string
	CSRFToken  string
	Error      string
}

// retagForm renders the retag form for one source tag. The form is the
// response body: it posts back to this URL with plain form semantics too
// (method/action), so it works without HTMX as well.
func (h *RetagHandler) retagForm(w http.ResponseWriter, r *http.Request) {
	name, tag := RepoPathParam(r), chi.URLParam(r, "tag")
	ctx := r.Context()
	repo, err := h.q.GetRepositoryByName(ctx, name)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if _, err := h.q.GetTag(ctx, db.GetTagParams{RepoID: repo.ID, Name: tag}); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	_ = RetagForm(retagFormData{
		RepoName:   name,
		TagName:    tag,
		TargetRepo: name,
		CSRFToken:  sessionCSRF(r),
	}).Render(ctx, w)
}

// retag copies the manifest behind one tag to a new tag name (plan task 21).
// The source tag is never touched: this is a copy, not a move. The new tag
// may live in this repository or in another one; across repositories the
// manifest is copied too — what a push of the same manifest would write —
// and an existing target tag is overwritten (UPSERT, as the plan defines).
func (h *RetagHandler) retag(w http.ResponseWriter, r *http.Request) {
	name, tag := RepoPathParam(r), chi.URLParam(r, "tag")
	r.Body = http.MaxBytesReader(w, r.Body, maxRetagFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	newTag := strings.TrimSpace(r.FormValue("new_tag"))
	targetName := strings.TrimSpace(r.FormValue("target_repo"))
	if targetName == "" {
		targetName = name // form default: same repository
	}
	if !api.ValidTag(newTag) {
		h.retagFormError(w, r, retagFormData{
			RepoName:   name,
			TagName:    tag,
			TargetRepo: targetName,
			CSRFToken:  sessionCSRF(r),
			Error:      "new tag must match [A-Za-z0-9_][A-Za-z0-9_.-]{0,127}",
		})
		return
	}
	if !api.ValidName(targetName) {
		h.retagFormError(w, r, retagFormData{
			RepoName:   name,
			TagName:    tag,
			TargetRepo: targetName,
			CSRFToken:  sessionCSRF(r),
			Error:      "target repository is not a valid repository name",
		})
		return
	}

	ctx := r.Context()
	src, err := h.q.GetRepositoryByName(ctx, name)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	manifest, err := h.q.GetManifestByTag(ctx, db.GetManifestByTagParams{RepoID: src.ID, Name: tag})
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	dst, err := h.targetRepo(ctx, targetName)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	copier := manifestCopier{q: h.q, dst: dst, seen: map[string]bool{}}
	if err := copier.copyInto(ctx, manifest); err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if err := h.q.SetTag(ctx, db.SetTagParams{
		RepoID:         dst.ID,
		Name:           newTag,
		ManifestDigest: manifest.Digest,
		UpdatedAt:      time.Now().Unix(),
	}); err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	audit(h.q, r, auditActionTagRetag, name+":"+tag+"->"+dst.Name+":"+newTag)
	h.retagRespond(w, r, dst.Name)
}

// targetRepo resolves the repository a retag writes into, creating it when
// it does not exist yet — the same upsert a manifest push performs, so
// "retag into another repository" needs no prior push to that name.
func (h *RetagHandler) targetRepo(ctx context.Context, name string) (db.Repository, error) {
	repo, err := h.q.GetRepositoryByName(ctx, name)
	if errors.Is(err, sql.ErrNoRows) {
		return h.q.CreateRepository(ctx, name)
	}
	return repo, err
}

// retagFormError answers a retag whose form input must be fixed. HTMX
// callers get the form back with the message (200 — HTMX swaps only 2xx, so
// the operator sees what to fix); plain callers get a 400 with the reason.
func (h *RetagHandler) retagFormError(w http.ResponseWriter, r *http.Request, data retagFormData) {
	if isHTMX(r) {
		_ = RetagForm(data).Render(r.Context(), w)
		return
	}
	http.Error(w, data.Error, http.StatusBadRequest)
}

// retagRespond answers a completed retag by navigating to the target
// repository's tag list, where the new tag is visible. HTMX callers get an
// HX-Redirect for the same reason the logout flow does it: a 303 followed by
// XHR would swap the tag-list page into the form's swap target instead of
// navigating. Plain callers get the 303 itself.
func (h *RetagHandler) retagRespond(w http.ResponseWriter, r *http.Request, targetName string) {
	location := "/dashboard/repos/" + targetName
	if isHTMX(r) {
		w.Header().Set("HX-Redirect", location)
		w.WriteHeader(http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, location, http.StatusSeeOther)
}

// manifestCopier copies a manifest and everything it references from the
// manifest's own repository into dst. seen guards against reference cycles
// in corrupt metadata — content addressing makes real cycles impossible.
type manifestCopier struct {
	q    db.Querier
	dst  db.Repository
	seen map[string]bool
}

// copyInto writes m and the manifest rows it references into the
// destination: manifests are scoped per repository (the tags foreign key
// demands it), so a retag into another repo must land what a push of the
// same manifest would — the manifest row plus one blob_refs edge per digest
// it references. Blob storage is untouched: blobs are content-addressed and
// shared across repositories, like a cross-repo mount. A row the destination
// already holds is left alone — the same digest is the same content.
func (c *manifestCopier) copyInto(ctx context.Context, m db.Manifest) error {
	if c.seen[m.Digest] {
		return nil
	}
	c.seen[m.Digest] = true
	if _, err := c.q.GetManifest(ctx, db.GetManifestParams{RepoID: c.dst.ID, Digest: m.Digest}); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err := c.q.UpsertManifest(ctx, db.UpsertManifestParams{
		RepoID:    c.dst.ID,
		Digest:    m.Digest,
		MediaType: m.MediaType,
		Payload:   m.Payload,
		Size:      m.Size,
	}); err != nil {
		return err
	}
	for _, digest := range manifestBlobDigests(m) {
		if err := c.q.AddBlobRef(ctx, db.AddBlobRefParams{Digest: digest, RepoID: c.dst.ID}); err != nil {
			return err
		}
		// A referenced digest the source repo stores as a manifest is a child
		// manifest (an index entry), not a blob: copy it too, so the
		// destination can serve the whole image.
		child, err := c.q.GetManifest(ctx, db.GetManifestParams{RepoID: m.RepoID, Digest: digest})
		switch {
		case errors.Is(err, sql.ErrNoRows):
			continue
		case err != nil:
			return err
		}
		if err := c.copyInto(ctx, child); err != nil {
			return err
		}
	}
	return nil
}

// manifestBlobDigests lists the digests one manifest references: its config
// and layers for an image manifest, its entries for an index — the exact set
// a manifest PUT records in blob_refs (plan task 9). An unparseable payload
// references nothing knowable and yields no edges.
func manifestBlobDigests(m db.Manifest) []string {
	d := BuildManifestDisplay(m)
	digests := make([]string, 0, 1+len(d.Layers))
	if d.ConfigDigest != "" {
		digests = append(digests, d.ConfigDigest)
	}
	for _, layer := range d.Layers {
		if layer.Digest != "" {
			digests = append(digests, layer.Digest)
		}
	}
	return digests
}
