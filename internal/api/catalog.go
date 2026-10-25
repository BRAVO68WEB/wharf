package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/BRAVO68WEB/wharf/internal/db"
	"github.com/BRAVO68WEB/wharf/internal/storage"
)

// defaultPageSize bounds a listing when the client sends no ?n=.
const defaultPageSize int64 = 100

// CatalogHandler serves the registry listing endpoints: GET /v2/_catalog
// (repositories) and GET /v2/{name}/tags/list (tags). Both paginate with the
// OCI keyset contract — ?n= bounds the page and ?last= names the entry to
// continue after — and advertise the next page with an RFC 5988 Link header.
type CatalogHandler struct {
	q db.Querier
}

// NewCatalogHandler returns a handler over the catalog rows in q.
func NewCatalogHandler(q db.Querier) *CatalogHandler {
	return &CatalogHandler{q: q}
}

// Mount registers the listing routes on r, a /v2 route tree. A repository
// name containing slashes reaches {name} through the collapse NewRouter
// installs.
func (h *CatalogHandler) Mount(r chi.Router) {
	r.Get("/_catalog", h.catalog)
	r.Get("/{name}/tags/list", h.tags)
}

// catalogResponse is the GET /v2/_catalog body:
// {"repositories":["name",...]}.
type catalogResponse struct {
	Repositories []string `json:"repositories"`
}

// tagsResponse is the GET /v2/{name}/tags/list body:
// {"name":"name","tags":["tag",...]}.
type tagsResponse struct {
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

// catalog answers GET /v2/_catalog with one page of repository names in
// lexical order.
func (h *CatalogHandler) catalog(w http.ResponseWriter, r *http.Request) {
	n, last := pageParams(r)

	var (
		repos []db.Repository
		err   error
	)
	if last == "" {
		repos, err = h.q.ListRepositoriesFromStart(r.Context(), n+1)
	} else {
		repos, err = h.q.ListRepositories(r.Context(), db.ListRepositoriesParams{After: last, Limit: n + 1})
	}
	if err != nil {
		WriteError(w, internalFailure())
		return
	}

	names := make([]string, 0, len(repos))
	for _, repo := range repos {
		names = append(names, repo.Name)
	}
	writeJSON(w, catalogResponse{Repositories: trimAndLink(w, r, n, names)})
}

// tags answers GET /v2/{name}/tags/list with one page of tag names in
// lexical order. A repository the registry does not hold answers 404 with
// the NAME_INVALID code the plan pins for this route.
func (h *CatalogHandler) tags(w http.ResponseWriter, r *http.Request) {
	name := RepoName(r)
	repo, err := h.q.GetRepositoryByName(r.Context(), name)
	if errors.Is(err, sql.ErrNoRows) {
		WriteErrorStatus(w, http.StatusNotFound,
			storage.NewError(storage.ErrorCodeNameInvalid, "repository name not known to the registry"))
		return
	}
	if err != nil {
		WriteError(w, internalFailure())
		return
	}

	n, last := pageParams(r)
	var rows []db.Tag
	if last == "" {
		rows, err = h.q.ListTagsByRepoFromStart(r.Context(), db.ListTagsByRepoFromStartParams{
			RepoID: repo.ID, Limit: n + 1,
		})
	} else {
		rows, err = h.q.ListTagsByRepo(r.Context(), db.ListTagsByRepoParams{
			RepoID: repo.ID, After: last, Limit: n + 1,
		})
	}
	if err != nil {
		WriteError(w, internalFailure())
		return
	}

	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, row.Name)
	}
	writeJSON(w, tagsResponse{Name: name, Tags: trimAndLink(w, r, n, names)})
}

// pageParams parses the listing query: n bounds the page (default
// defaultPageSize) and last is the name to continue after, empty at the
// start. An unusable n falls back to the default rather than failing the
// request; the listing stays bounded either way.
func pageParams(r *http.Request) (n int64, last string) {
	n = defaultPageSize
	if raw := r.URL.Query().Get("n"); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed > 0 {
			// Clamp so the lookahead limit n+1 cannot overflow into a
			// negative LIMIT, which SQLite reads as "no limit at all".
			n = min(parsed, math.MaxInt64-1)
		}
	}
	return n, r.URL.Query().Get("last")
}

// trimAndLink cuts names down to one page of n rows and, when rows remain,
// advertises the next page with the RFC 5988 Link header:
//
//	Link: </v2/_catalog?n=2&last=bar>; rel="next"
func trimAndLink(w http.ResponseWriter, r *http.Request, n int64, names []string) []string {
	if int64(len(names)) > n {
		names = names[:n]
		next := fmt.Sprintf("<%s?n=%d&last=%s>; rel=\"next\"",
			r.URL.Path, n, url.QueryEscape(names[len(names)-1]))
		w.Header().Set("Link", next)
	}
	return names
}

// writeJSON writes v as the JSON body of a listing response.
func writeJSON(w http.ResponseWriter, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		WriteError(w, internalFailure())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
