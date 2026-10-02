package dashboard

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/user/better-docker-registry/internal/db"
)

// ManifestsHandler serves the manifest detail view for one repository.
type ManifestsHandler struct {
	q db.Querier
}

// NewManifestsHandler returns a handler over the manifests recorded in q.
func NewManifestsHandler(q db.Querier) *ManifestsHandler {
	return &ManifestsHandler{q: q}
}

// Mount registers the manifest detail route on r (a subrouter mounted at the
// repo path prefix). Wrap r with CollapseRepoName so multi-segment repository
// names arrive intact.
func (h *ManifestsHandler) Mount(r chi.Router) {
	r.Get("/{name}/manifests/{digest}", h.showManifest)
}

// showManifest renders the structured manifest detail: media type, config
// digest and layer list. The raw JSON payload is never displayed. HTMX
// callers get a fragment for the #manifest-detail container; direct
// navigation gets a full page.
func (h *ManifestsHandler) showManifest(w http.ResponseWriter, r *http.Request) {
	name := RepoPathParam(r)
	digest := chi.URLParam(r, "digest")
	if name == "" || digest == "" {
		http.NotFound(w, r)
		return
	}

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

	m, err := h.q.GetManifest(ctx, db.GetManifestParams{RepoID: repo.ID, Digest: digest})
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	csrf := sessionCSRF(r)
	if !json.Valid(m.Payload) {
		if isHTMX(r) {
			_ = ManifestError("manifest payload is not valid JSON").Render(ctx, w)
			return
		}
		_ = ManifestErrorPage("manifest payload is not valid JSON").Render(ctx, w)
		return
	}

	d := BuildManifestDisplay(m)
	if isHTMX(r) {
		_ = ManifestDetail(name, d, csrf).Render(ctx, w)
		return
	}
	_ = ManifestDetailPage(name, d, csrf).Render(ctx, w)
}

// manifestDisplay is the structured view of one manifest. Only parsed fields
// are shown; the raw payload is never rendered.
type manifestDisplay struct {
	Digest       string
	MediaType    string
	Size         int64
	ConfigDigest string
	Layers       []layerDisplay
}

// layerDisplay is one entry of a manifest's layer list (or, for an index,
// one child-manifest entry).
type layerDisplay struct {
	Digest    string
	Size      int64
	MediaType string
}

// manifestPayload mirrors the JSON shape of an OCI/Docker manifest or index.
// Only the fields the detail view shows are decoded.
type manifestPayload struct {
	Config    *payloadDescriptor  `json:"config"`
	Layers    []payloadDescriptor `json:"layers"`
	Manifests []payloadDescriptor `json:"manifests"`
}

// payloadDescriptor is one OCI descriptor with the fields the detail view
// shows: content address, size and media type.
type payloadDescriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

// buildManifestDisplay turns a stored manifest row into the structured view.
// An image manifest contributes its config digest and layer list; an index
// contributes its child-manifest list in the same layer table.
func BuildManifestDisplay(m db.Manifest) manifestDisplay {
	d := manifestDisplay{
		Digest:    m.Digest,
		MediaType: m.MediaType,
		Size:      m.Size,
	}

	var p manifestPayload
	if err := json.Unmarshal(m.Payload, &p); err != nil {
		return d // already guarded by json.Valid; defensive only
	}

	if p.Config != nil {
		d.ConfigDigest = p.Config.Digest
	}

	entries := p.Layers
	if entries == nil {
		entries = p.Manifests
	}
	d.Layers = make([]layerDisplay, 0, len(entries))
	for _, e := range entries {
		d.Layers = append(d.Layers, layerDisplay{
			Digest:    e.Digest,
			Size:      e.Size,
			MediaType: e.MediaType,
		})
	}
	return d
}
