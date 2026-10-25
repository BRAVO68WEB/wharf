package dashboard

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/user/better-docker-registry/internal/api"
	"github.com/user/better-docker-registry/internal/db"
)

// maxRetagFormBytes bounds the retag body. Two short text fields do not
// need more, and the decoder must not read unbounded input.
const maxRetagFormBytes = 8 << 10

// RetagHandler copies the manifest behind one tag to a new tag name. The
// source tag is never touched. One source tag per request.
type RetagHandler struct {
	q db.Querier
}

// NewRetagHandler returns a handler over the metadata recorded in q.
func NewRetagHandler(q db.Querier) *RetagHandler {
	return &RetagHandler{q: q}
}

// copyTag copies the manifest behind tag onto newTag in targetName. An empty
// targetName means the source repository. The source tag is never touched.
// status is 200, 400, 404, or 500. On 400, msg is the validation text.
func (h *RetagHandler) copyTag(r *http.Request, name, tag, targetName, newTag string) (string, int, string) {
	if targetName == "" {
		targetName = name
	}
	if !api.ValidTag(newTag) {
		return "", http.StatusBadRequest, "new tag must match [A-Za-z0-9_][A-Za-z0-9_.-]{0,127}"
	}
	if !api.ValidName(targetName) {
		return "", http.StatusBadRequest, "target repository is not a valid repository name"
	}

	ctx := r.Context()
	src, err := h.q.GetRepositoryByName(ctx, name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", http.StatusNotFound, ""
	}
	if err != nil {
		return "", http.StatusInternalServerError, ""
	}
	manifest, err := h.q.GetManifestByTag(ctx, db.GetManifestByTagParams{RepoID: src.ID, Name: tag})
	if errors.Is(err, sql.ErrNoRows) {
		return "", http.StatusNotFound, ""
	}
	if err != nil {
		return "", http.StatusInternalServerError, ""
	}

	dst, err := h.targetRepo(ctx, targetName)
	if err != nil {
		return "", http.StatusInternalServerError, ""
	}
	copier := manifestCopier{q: h.q, dst: dst, seen: map[string]bool{}}
	if err := copier.copyInto(ctx, manifest); err != nil {
		return "", http.StatusInternalServerError, ""
	}
	if err := h.q.SetTag(ctx, db.SetTagParams{
		RepoID:         dst.ID,
		Name:           newTag,
		ManifestDigest: manifest.Digest,
		UpdatedAt:      time.Now().Unix(),
	}); err != nil {
		return "", http.StatusInternalServerError, ""
	}
	audit(h.q, r, auditActionTagRetag, name+":"+tag+"->"+dst.Name+":"+newTag)
	return dst.Name, http.StatusOK, ""
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
// a manifest PUT records in blob_refs. An unparseable payload references
// nothing knowable and yields no edges.
func manifestBlobDigests(m db.Manifest) []string {
	d := parseManifest(m)
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
