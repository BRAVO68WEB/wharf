package dashboard

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/user/better-docker-registry/internal/db"
)

// maxTagRows bounds the tag list page. A dashboard table past this many rows
// is unreadable anyway; the registry API's /tags/list is the bulk surface.
const maxTagRows = 500

// TagsHandler serves the tag list page for one repository.
type TagsHandler struct {
	q db.Querier
}

// NewTagsHandler returns a handler over the tags recorded in q.
func NewTagsHandler(q db.Querier) *TagsHandler {
	return &TagsHandler{q: q}
}

// Mount registers the tag list route on r (a subrouter mounted at the repo
// path prefix). Wrap r with CollapseRepoName so multi-segment repository
// names reach the handler intact.
func (h *TagsHandler) Mount(r chi.Router) {
	r.Get("/{name}", h.listTags)
}

// listTags renders the tag table for one repository. HTMX callers get just
// the table fragment; direct navigation gets the full page.
func (h *TagsHandler) listTags(w http.ResponseWriter, r *http.Request) {
	name := RepoPathParam(r)
	if name == "" {
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

	rows, err := h.q.ListTagsByRepoFromStart(ctx, db.ListTagsByRepoFromStartParams{
		RepoID: repo.ID,
		Limit:  maxTagRows,
	})
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	tags := make([]tagDisplay, 0, len(rows))
	sizes := make(map[string]int64, len(rows))
	for _, row := range rows {
		size, ok := sizes[row.ManifestDigest]
		if !ok {
			m, merr := h.q.GetManifest(ctx, db.GetManifestParams{
				RepoID: repo.ID,
				Digest: row.ManifestDigest,
			})
			if merr != nil {
				if !errors.Is(merr, sql.ErrNoRows) {
					http.Error(w, "internal server error", http.StatusInternalServerError)
					return
				}
				size = -1 // dangling tag: manifest row missing
			} else {
				size = m.Size
			}
			sizes[row.ManifestDigest] = size
		}
		tags = append(tags, tagDisplay{
			Name:           row.Name,
			ManifestDigest: row.ManifestDigest,
			UpdatedAt:      time.Unix(row.UpdatedAt, 0),
			Size:           size,
		})
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	csrf := sessionCSRF(r)
	if isHTMX(r) {
		_ = TagsTable(name, tags, csrf).Render(ctx, w)
		return
	}
	_ = TagsPage(name, tags, csrf).Render(ctx, w)
}

// tagDisplay is one row of the tag table. Size is the manifest payload size
// in bytes, or -1 when the manifest row is missing.
type tagDisplay struct {
	Name           string
	ManifestDigest string
	UpdatedAt      time.Time
	Size           int64
}

// FormatBytes renders a byte count as a short human-readable string.
func FormatBytes(n int64) string {
	if n < 0 {
		return "—"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// isHTMX reports whether the request comes from an HTMX swap (the caller
// wants a fragment, not a full page).
func isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}
