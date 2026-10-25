package dashboard

import (
	"net/http"
	"strconv"
	"strings"
)

// repoPageSize is one page of the repository catalog.
const repoPageSize = 20

// maxSearchLen bounds the operator's filter string before it reaches SQL, so
// a pasted blob cannot turn into a pathological LIKE pattern.
const maxSearchLen = 200

// parseRepoQuery reads the catalog filter. Unknown or hostile values degrade
// to the default rather than a 400: the query is a bounding hint, so a typo
// must not take the page down. ParseInt with bitSize 32 keeps
// (page-1)*repoPageSize far below MaxInt64, so the OFFSET can never overflow
// into SQLite's unbounded read of a negative limit.
func parseRepoQuery(r *http.Request) (search string, page int) {
	query := r.URL.Query()
	search = strings.TrimSpace(query.Get("search"))
	if len(search) > maxSearchLen {
		search = search[:maxSearchLen]
	}
	page = 1
	if raw := query.Get("page"); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 32); err == nil && n > 0 {
			page = int(n)
		}
	}
	return search, page
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
