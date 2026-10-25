package api

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/BRAVO68WEB/wharf/internal/db"
	"github.com/BRAVO68WEB/wharf/internal/repopath"
	"github.com/BRAVO68WEB/wharf/internal/storage"
)

// NewRouter builds the registry API route tree: every endpoint the Docker and
// OCI clients address under /v2. The tree carries no dashboard middleware; the
// dashboard JSON API mounts on its own router.
//
// Repository names may contain slashes ("library/nginx"), which a chi path
// parameter cannot capture. collapseRepoName therefore rewrites the routing
// path so the name arrives as a single segment, and handlers read the real
// name back with RepoName.
//
// Options are optional so existing call sites keep compiling; see
// WithWriteQuiesce for the GC integration seam.
func NewRouter(store storage.Storage, q db.Querier, opts ...RouterOption) http.Handler {
	cfg := routerConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}

	v2 := chi.NewRouter()
	v2.Use(WithAPIVersion)
	if cfg.writeQuiesce != nil {
		v2.Use(cfg.writeQuiesce)
	}
	if cfg.jwks != nil {
		v2.Get("/_keys", cfg.jwks.ServeHTTP)
	}
	NewBlobHandler(store, q).Mount(v2)
	NewUploadHandler(store, q, cfg.maxUploadSize).Mount(v2)
	NewManifestHandler(q).Mount(v2)
	NewCatalogHandler(q).Mount(v2)
	NewReferrersHandler(q).Mount(v2)

	r := chi.NewRouter()
	r.Mount("/v2", collapseRepoName(v2))
	return r
}

// RouterOption customises NewRouter.
type RouterOption func(*routerConfig)

type routerConfig struct {
	writeQuiesce  func(http.Handler) http.Handler
	jwks          http.Handler
	maxUploadSize int64
}

// WithWriteQuiesce guards every state-changing /v2 request with quiesce, the
// GC quiesce check (plan tasks 18-19: "during GC, return 503 on /v2/* write
// requests"). Pass gc.Lock.WriteGuard; safe methods inside the guard pass
// through, so pulls stay up while a collection sweeps. Without this option the
// tree behaves exactly as before (no quiesce).
func WithWriteQuiesce(quiesce func(http.Handler) http.Handler) RouterOption {
	return func(c *routerConfig) {
		c.writeQuiesce = quiesce
	}
}

// WithJWKS serves the token signing public keys at GET /v2/_keys from the
// given handler (auth.Signer.ServeJWKS). The api tree stays free of an auth
// import; the wiring task passes the handler in.
func WithJWKS(jwks http.Handler) RouterOption {
	return func(c *routerConfig) {
		c.jwks = jwks
	}
}

// WithMaxUploadSize caps a single blob upload in bytes; zero is unlimited.
func WithMaxUploadSize(n int64) RouterOption {
	return func(c *routerConfig) {
		c.maxUploadSize = n
	}
}

// WithAPIVersion sets the Docker-Distribution-API-Version header every
// registry API response must carry.
func WithAPIVersion(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(storage.HeaderAPIVersion, storage.APIVersion)
		next.ServeHTTP(w, r)
	})
}

// collapseRepoName rewrites the /v2-relative routing path of a request so the
// repository name is one path segment. The request itself is untouched:
// handlers recover the name with RepoName.
func collapseRepoName(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rctx := chi.RouteContext(r.Context()); rctx != nil {
			if collapsed, ok := collapseRepoPath(rctx.RoutePath); ok {
				rctx.RoutePath = collapsed
			}
		}
		next.ServeHTTP(w, r)
	})
}

// collapseRepoPath rewrites "/{name}/blobs/{digest}" into
// "/{escaped name}/blobs/{digest}". It reports false when the path names no
// operation (the catalog) or no repository at all; those route unchanged.
func collapseRepoPath(path string) (string, bool) {
	name, ok := repopath.Name(path)
	if !ok {
		return path, false
	}
	suffix, ok := repopath.Suffix(path)
	if !ok {
		return path, false
	}
	return "/" + url.PathEscape(name) + suffix, true
}

// RepoName returns the repository name of the matched /v2 route, with the
// slashes collapseRepoName escaped given back.
func RepoName(r *http.Request) string {
	raw := chi.URLParam(r, "name")
	name, err := url.PathUnescape(raw)
	if err != nil {
		return raw
	}
	return name
}

// namePattern is the repository name grammar of the OCI distribution spec.
var namePattern = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*(?:/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)*$`)

// ValidName reports whether name is a syntactically valid repository name.
func ValidName(name string) bool {
	return name != "" && len(name) <= 255 && namePattern.MatchString(name)
}

// digestPattern accepts the digest algorithms the storage drivers verify:
// sha256 (the canonical OCI algorithm) and sha512 (the other algorithm the
// OCI conformance suite exercises).
var digestPattern = regexp.MustCompile(`^(sha256:[a-f0-9]{64}|sha512:[a-f0-9]{128})$`)

// ValidDigest reports whether digest is a syntactically valid content digest.
func ValidDigest(digest string) bool {
	return digestPattern.MatchString(digest)
}

// DigestAlgo returns the hash algorithm named by a validated digest
// ("sha256" or "sha512"). Callers pass ValidDigest values.
func DigestAlgo(digest string) string {
	algo, _, _ := strings.Cut(digest, ":")
	return algo
}

// tagPattern is the tag grammar of the OCI distribution spec: [A-Za-z0-9_]
// followed by up to 127 more of [A-Za-z0-9_.-].
var tagPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)

// ValidTag reports whether tag is a syntactically valid tag.
func ValidTag(tag string) bool {
	return tag != "" && tagPattern.MatchString(tag)
}
