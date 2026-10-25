// Package site serves the dashboard single-page app from the files embedded
// in the binary. Hashed build assets are cached; every other dashboard path
// falls back to index.html so the client router can own it. /dashboard/api
// is never served from here: a missing API route must stay a 404, not the app.
package site

import (
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

const dashboardPrefix = "/dashboard"

// Handler serves GET and HEAD for the dashboard UI.
func Handler() http.Handler {
	fsys, err := siteFS()
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
		})
	}
	return &handler{fsys: fsys}
}

type handler struct {
	fsys fs.FS
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// The JSON API is registered on its own routes. If a request still lands
	// here, answer 404 rather than the app shell: a fallback that swallows
	// /dashboard/api would hide a missing endpoint behind index.html.
	if r.URL.Path == dashboardPrefix+"/api" || strings.HasPrefix(r.URL.Path, dashboardPrefix+"/api/") {
		http.NotFound(w, r)
		return
	}

	rel := strings.TrimPrefix(r.URL.Path, dashboardPrefix)
	rel = strings.TrimPrefix(path.Clean("/"+rel), "/")
	if rel == "" || rel == "." || rel == "index.html" {
		h.serveIndex(w, r)
		return
	}
	if strings.HasPrefix(rel, "../") || rel == ".." {
		http.NotFound(w, r)
		return
	}

	f, err := h.fsys.Open(rel)
	if err != nil {
		if hasExt(rel) {
			http.NotFound(w, r)
			return
		}
		h.serveIndex(w, r)
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil || stat.IsDir() {
		if hasExt(rel) {
			http.NotFound(w, r)
			return
		}
		h.serveIndex(w, r)
		return
	}
	if strings.HasPrefix(rel, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	rs, ok := f.(io.ReadSeeker)
	if !ok {
		http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
		return
	}
	http.ServeContent(w, r, stat.Name(), stat.ModTime(), rs)
}

func (h *handler) serveIndex(w http.ResponseWriter, r *http.Request) {
	f, err := h.fsys.Open("index.html")
	if err != nil {
		http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	rs, ok := f.(io.ReadSeeker)
	if !ok {
		http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "index.html", time.Time{}, rs)
}

func hasExt(rel string) bool {
	base := path.Base(rel)
	return strings.Contains(base, ".")
}
