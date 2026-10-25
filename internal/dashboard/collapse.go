package dashboard

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
)

// CollapseRepoName wraps a dashboard subrouter so repository names containing
// slashes ("library/nginx") reach handlers as one chi path parameter. Mount
// the result at the repo path prefix:
//
//	repos := chi.NewRouter()
//	r.Mount("/api/repo", CollapseRepoName(repos))
//
// The request URL is untouched; handlers read the real name back with
// RepoPathParam.
func CollapseRepoName(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rctx := chi.RouteContext(r.Context()); rctx != nil {
			if collapsed, ok := collapseRepoPath(rctx.RoutePath); ok {
				rctx.RoutePath = collapsed
			}
		}
		next.ServeHTTP(w, r)
	})
}

// collapseRepoPath rewrites "/{name}", "/{name}/tags/{tag}" and
// "/{name}/manifests/{ref}" so the name is one escaped segment. The name
// boundary is the LAST "/tags/" or "/manifests/" substring — the same cut
// internal/api uses — without one the whole remainder is the name.
//
// Ambiguity left open: a repository whose own name contains a "/tags/" or
// "/manifests/" segment ("acme/tags/prod") collides with the tag/manifest
// route shape and 404s; no valid route can express both readings at once.
func collapseRepoPath(path string) (string, bool) {
	if !strings.HasPrefix(path, "/") {
		return path, false
	}
	rest := path[1:]
	if rest == "" {
		return path, false
	}

	cut := strings.LastIndex(rest, "/manifests/")
	if t := strings.LastIndex(rest, "/tags/"); t > cut {
		cut = t
	}
	if cut < 0 {
		return "/" + url.PathEscape(rest), true
	}
	name := rest[:cut]
	if name == "" {
		return path, false
	}
	return "/" + url.PathEscape(name) + rest[cut:], true
}

// RepoPathParam returns the repository name of the matched dashboard route,
// with the slashes CollapseRepoName escaped given back.
func RepoPathParam(r *http.Request) string {
	raw := chi.URLParam(r, "name")
	name, err := url.PathUnescape(raw)
	if err != nil {
		return raw
	}
	return name
}
