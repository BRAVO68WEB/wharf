package dashboard

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"

	"github.com/user/better-docker-registry/internal/db"
	"github.com/user/better-docker-registry/templates"
)

// repoPageSize is one page of the repository table. Past this many rows the
// table stops being scannable; the registry API's /_catalog is the bulk
// surface.
const repoPageSize = 20

// maxSearchLen bounds the operator's filter string before it reaches SQL, so
// a pasted blob cannot turn into a pathological LIKE pattern.
const maxSearchLen = 200

// RepoHandler serves the repository browse pages (plan task 15): the full
// page at /dashboard and the swappable table fragment at /dashboard/repos.
type RepoHandler struct {
	q db.Querier
}

// NewRepoHandler returns a handler over the repositories recorded in q.
func NewRepoHandler(q db.Querier) *RepoHandler {
	return &RepoHandler{q: q}
}

// Mount registers the browse routes on r.
//
// Paths are absolute, matching auth.SessionManager.Mount — register this on
// the root mux, not inside a /dashboard subrouter.
func (h *RepoHandler) Mount(r chi.Router) {
	r.Get("/dashboard", h.servePage)
	r.Get("/dashboard/repos", h.serveResults)
}

// servePage renders the full page: base layout plus the browse panel.
func (h *RepoHandler) servePage(w http.ResponseWriter, r *http.Request) {
	data, err := h.load(r)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	component := templates.Layout(templates.Chrome{
		Title:     "Repositories",
		Username:  sessionUser(r),
		CSRFToken: sessionCSRF(r),
	}, templates.RepoPage(data))
	render(w, r, component)
}

// serveResults renders only the results fragment. HTMX swaps it into
// #repo-results for live search and pagination, keeping the filter input's
// focus intact.
func (h *RepoHandler) serveResults(w http.ResponseWriter, r *http.Request) {
	data, err := h.load(r)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	render(w, r, templates.RepoResults(data))
}

// load reads the filter and paging parameters off the query string and fetches
// one page of repositories with their tag/manifest counts.
//
// Unknown or hostile values degrade to the default rather than a 400: the
// query is a bounding hint (see the ?n= precedent in internal/api/catalog.go),
// so a typo must not take the page down. A page past the end simply renders an
// empty table with a working Previous link.
func (h *RepoHandler) load(r *http.Request) (templates.RepoList, error) {
	query := r.URL.Query()

	search := strings.TrimSpace(query.Get("search"))
	if len(search) > maxSearchLen {
		search = search[:maxSearchLen]
	}

	// ParseInt with bitSize 32 keeps (page-1)*repoPageSize far below
	// MaxInt64, so the OFFSET can never overflow into SQLite's unbounded
	// read of a negative limit.
	page := 1
	if raw := query.Get("page"); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 32); err == nil && n > 0 {
			page = int(n)
		}
	}

	pattern := repoSearchPattern(search)
	ctx := r.Context()

	total, err := h.q.CountRepositoriesMatching(ctx, pattern)
	if err != nil {
		return templates.RepoList{}, err
	}

	rows, err := h.q.ListRepositoriesWithStats(ctx, db.ListRepositoriesWithStatsParams{
		Pattern: pattern,
		Offset:  int64(page-1) * repoPageSize,
		Limit:   repoPageSize,
	})
	if err != nil {
		return templates.RepoList{}, err
	}

	repos := make([]templates.RepoRow, 0, len(rows))
	for _, row := range rows {
		repos = append(repos, templates.RepoRow{
			Name:          row.Name,
			TagCount:      row.TagCount,
			ManifestCount: row.ManifestCount,
			CreatedAt:     time.Unix(row.CreatedAt, 0),
		})
	}

	return templates.RepoList{
		Repos:      repos,
		Search:     search,
		Page:       page,
		PageSize:   repoPageSize,
		TotalCount: total,
	}, nil
}

// repoSearchPattern turns the operator's filter text into a LIKE pattern whose
// wildcards are escaped, so the box matches substrings literally ("a.b" finds
// "a.b", never "axb"). An empty filter matches everything.
func repoSearchPattern(search string) string {
	if search == "" {
		return "%"
	}
	var b strings.Builder
	b.Grow(len(search) + 2)
	b.WriteByte('%')
	for _, r := range search {
		switch r {
		case '%', '_', '\\':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('%')
	return b.String()
}

// render writes a templ component as an HTML document. Rendering only fails
// when the write to w fails (client gone): there is no status left to correct
// and nothing useful to report, so the error stops at this boundary.
func render(w http.ResponseWriter, r *http.Request, component templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := component.Render(r.Context(), w); err != nil {
		return
	}
}
