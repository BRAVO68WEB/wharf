// Package templates holds the templ components and view models for the HTMX
// dashboard. Components are rendered by internal/dashboard handlers; the
// generated *_templ.go files are produced by `templ generate` and committed
// alongside their .templ sources.
package templates

import (
	"encoding/json"
	"net/url"
	"strconv"
	"time"

	"github.com/user/better-docker-registry/internal/middleware"
)

// Chrome is the page state every dashboard view shares: the document title,
// the signed-in operator shown in the nav bar (empty hides it) and the session
// CSRF token the shell renders into its mutating controls.
type Chrome struct {
	Title     string
	Username  string
	CSRFToken string
}

// RepoRow is one repository in the browse table (plan task 15). Counts are
// pre-aggregated by db.ListRepositoriesWithStats so the template never issues
// a query.
type RepoRow struct {
	Name          string
	TagCount      int64
	ManifestCount int64
	CreatedAt     time.Time
}

// RepoList is the view model for the repository browse panel: one page of
// rows plus the filter and pagination state the pager links carry forward.
type RepoList struct {
	Repos      []RepoRow
	Search     string
	Page       int
	PageSize   int
	TotalCount int64
}

// TotalPages is the number of pages the current filter spans, at least 1 so
// the pager always has a "page 1 of 1" to show.
func (l RepoList) TotalPages() int {
	if l.PageSize <= 0 || l.TotalCount <= 0 {
		return 1
	}
	pages := int((l.TotalCount + int64(l.PageSize) - 1) / int64(l.PageSize))
	if pages < 1 {
		return 1
	}
	return pages
}

// HasPrev/HasNext bound the pager; a page past the end simply shows no rows
// and offers Prev, which is the honest state rather than a clamped surprise.
func (l RepoList) HasPrev() bool { return l.Page > 1 }
func (l RepoList) HasNext() bool { return l.Page < l.TotalPages() }

// RepoHref is the tag-browse URL for a repository (plan task 16). Repository
// names carry slashes ("library/nginx"), so the name is appended as path
// segments rather than escaped into one.
func RepoHref(name string) string {
	return "/dashboard/repos/" + name
}

// ResultsHref builds the HTMX partial URL for a page of the current filter.
// The shape is the plan's `hx-get="/dashboard/repos?search=foo"` contract,
// with `page` added once pagination is in play.
func ResultsHref(search string, page int) string {
	values := url.Values{}
	if search != "" {
		values.Set("search", search)
	}
	if page > 1 {
		values.Set("page", strconv.Itoa(page))
	}
	encoded := values.Encode()
	if encoded == "" {
		return "/dashboard/repos"
	}
	return "/dashboard/repos?" + encoded
}

// CSRFHeaders renders the JSON object HTMX's hx-headers attribute expects,
// carrying the session CSRF token so state-changing dashboard requests satisfy
// middleware.CSRF (which reads only the X-CSRF-Token header).
func CSRFHeaders(token string) string {
	encoded, err := json.Marshal(map[string]string{middleware.CSRFHeader: token})
	if err != nil {
		// Unreachable for map[string]string; kept because json.Marshal
		// reports errors and a silent empty header map is the safest degrade.
		return "{}"
	}
	return string(encoded)
}
