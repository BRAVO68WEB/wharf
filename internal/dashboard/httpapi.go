package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BRAVO68WEB/wharf/internal/db"
	"github.com/BRAVO68WEB/wharf/internal/gc"
	"github.com/BRAVO68WEB/wharf/internal/storage"
)

// HTTPAPI is the JSON API the embedded dashboard calls. Paths are relative
// to the /dashboard mount: /api/repos for the catalog, /api/repo/{name} for
// one repository, and /api/gc. The SPA file server must not answer these.
type HTTPAPI struct {
	sqlDB    *sql.DB
	q        db.Querier
	store    storage.Storage
	gc       *GCHandler
	deleter  *DeleteHandler
	retagger *RetagHandler
	// pullHost is host[:port] for docker pull lines. Empty hides them.
	pullHost string
	// gcEvery is the scheduled collection interval. Zero means off.
	gcEvery time.Duration
	gcLast  atomic.Int64
}

// NewHTTPAPI returns the dashboard JSON API. lock is the same quiesce lock
// /v2 writes use, so a collection still refuses overlapping registry writes.
func NewHTTPAPI(sqlDB *sql.DB, q db.Querier, store storage.Storage, lock *gc.Lock, runnerOpts ...gc.RunnerOption) *HTTPAPI {
	return &HTTPAPI{
		sqlDB:    sqlDB,
		q:        q,
		store:    store,
		gc:       NewGCHandler(q, store, lock, runnerOpts...),
		deleter:  NewDeleteHandler(q),
		retagger: NewRetagHandler(q),
	}
}

// SetPullHost sets the host[:port] printed in docker pull lines. Empty hides
// the line. The value comes from server.public_url and has no scheme.
func (h *HTTPAPI) SetPullHost(host string) { h.pullHost = host }

// SetGCInterval records how often a scheduled collection runs. Zero means
// the dashboard is the only way to start one.
func (h *HTTPAPI) SetGCInterval(every time.Duration) { h.gcEvery = every }

// CollectIfIdle runs one collection when none is in flight. ran is false
// when another collection holds the lock.
func (h *HTTPAPI) CollectIfIdle(ctx context.Context) (ran bool, err error) {
	ran, err = gc.Collect(ctx, h.gc.lock, func(ctx context.Context) error {
		_, runErr := h.gc.runner.Run(ctx)
		return runErr
	})
	if ran && err == nil {
		h.gcLast.Store(time.Now().Unix())
	}
	return ran, err
}

// Mount registers the JSON routes on r. r is the authenticated /dashboard
// router. Repository names may contain slashes; CollapseRepoName keeps them
// as one parameter.
func (h *HTTPAPI) Mount(r chi.Router) {
	r.Get("/api/repos", h.listRepos)
	r.Post("/api/repos/publish", h.setPublished)
	r.Get("/api/accounts", h.listAccounts)
	r.Post("/api/accounts", h.createAccount)
	r.Delete("/api/accounts/{id}", h.deleteAccount)
	r.Post("/api/accounts/{id}/tokens", h.createAccountToken)
	r.Get("/api/tokens", h.listMyTokens)
	r.Post("/api/tokens", h.createMyToken)
	r.Delete("/api/tokens/{id}", h.revokeMyToken)
	r.Get("/api/gc", h.gcStats)
	r.Post("/api/gc/dry-run", h.gcDryRun)
	r.Post("/api/gc/run", h.gcRun)

	// /api/repo, not /api/repos. Mount registers the mount path itself, so a
	// subrouter at /api/repos would replace the catalog route. Collapse still
	// needs the /tags/ and /manifests/ boundaries, which this tree keeps.
	repos := chi.NewRouter()
	repos.Get("/{name}", h.listTags)
	repos.Get("/{name}/manifests/{digest}", h.showManifest)
	repos.Delete("/{name}/tags/{tag}", h.deleteTag)
	repos.Delete("/{name}/manifests/{digest}", h.deleteManifest)
	repos.Post("/{name}/tags/{tag}/retag", h.retag)
	r.Mount("/api/repo", CollapseRepoName(repos))
}

// MountPublic registers the catalog anonymous clients may read. It lists only
// repositories an operator has published.
func (h *HTTPAPI) MountPublic(r chi.Router) {
	r.Get("/api/public/repos", h.listPublicRepos)
}

type repoJSON struct {
	Name          string    `json:"name"`
	TagCount      int64     `json:"tagCount"`
	ManifestCount int64     `json:"manifestCount"`
	SizeBytes     int64     `json:"sizeBytes"`
	UpdatedAt     time.Time `json:"updatedAt"`
	Digest        string    `json:"digest,omitempty"`
	Published     bool      `json:"published"`
	LatestTag     string    `json:"latestTag,omitempty"`
	PullHost      string    `json:"pullHost,omitempty"`
}

type repoListJSON struct {
	Search   string     `json:"search"`
	Page     int        `json:"page"`
	PageSize int        `json:"pageSize"`
	Total    int64      `json:"total"`
	Mounted  *repoJSON  `json:"mounted"`
	Repos    []repoJSON `json:"repos"`
}

func (h *HTTPAPI) listRepos(w http.ResponseWriter, r *http.Request) {
	search, page := parseRepoQuery(r)
	pattern := repoSearchPattern(search)
	ctx := r.Context()

	var mounted *repoJSON
	skipMounted := false
	row, err := h.q.GetMountedRepository(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeAPIError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if err == nil {
		tag, digest, derr := h.latestTag(ctx, row.ID)
		if derr != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		size, serr := h.repoBytes(ctx, row.ID, row.SizeBytes)
		if serr != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		item := repoFromCatalog(row.Name, row.TagCount, row.ManifestCount, size, row.UpdatedAt, digest, row.Published)
		h.attachPull(&item, tag)
		mounted = &item
		// The mounted repository is the first row of this same ordering, so
		// the index skips one slot when the filter would have included it.
		// Search filters the index only; the mount stays put.
		skipMounted = catalogMatches(row.Name, search)
	}

	total, err := h.q.CountRepositoriesMatching(ctx, pattern)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	offset := int64(page-1) * repoPageSize
	if skipMounted {
		if total > 0 {
			total--
		}
		offset++
	}

	rows, err := h.q.ListCatalogRepositories(ctx, db.ListCatalogRepositoriesParams{
		Pattern: pattern,
		Offset:  offset,
		Limit:   repoPageSize,
	})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	repos := make([]repoJSON, 0, len(rows))
	for _, item := range rows {
		if mounted != nil && item.Name == mounted.Name {
			continue
		}
		tag, digest, derr := h.latestTag(ctx, item.ID)
		if derr != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		size, serr := h.repoBytes(ctx, item.ID, item.SizeBytes)
		if serr != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		row := repoFromCatalog(item.Name, item.TagCount, item.ManifestCount, size, item.UpdatedAt, digest, item.Published)
		h.attachPull(&row, tag)
		repos = append(repos, row)
	}

	writeJSON(w, http.StatusOK, repoListJSON{
		Search:   search,
		Page:     page,
		PageSize: repoPageSize,
		Total:    total,
		Mounted:  mounted,
		Repos:    repos,
	})
}

func repoFromCatalog(name string, tags, manifests, size, updated int64, digest string, published bool) repoJSON {
	return repoJSON{
		Name:          name,
		TagCount:      tags,
		ManifestCount: manifests,
		SizeBytes:     size,
		UpdatedAt:     time.Unix(updated, 0).UTC(),
		Digest:        digest,
		Published:     published,
	}
}

func (h *HTTPAPI) listPublicRepos(w http.ResponseWriter, r *http.Request) {
	search, page := parseRepoQuery(r)
	pattern := repoSearchPattern(search)
	ctx := r.Context()

	total, err := h.q.CountPublishedRepositories(ctx, pattern)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	rows, err := h.q.ListPublishedRepositories(ctx, db.ListPublishedRepositoriesParams{
		Pattern: pattern,
		Offset:  int64(page-1) * repoPageSize,
		Limit:   repoPageSize,
	})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	repos := make([]repoJSON, 0, len(rows))
	for _, item := range rows {
		tag, digest, derr := h.latestTag(ctx, item.ID)
		if derr != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		size, serr := h.repoBytes(ctx, item.ID, item.SizeBytes)
		if serr != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		row := repoFromCatalog(item.Name, item.TagCount, item.ManifestCount, size, item.UpdatedAt, digest, true)
		h.attachPull(&row, tag)
		repos = append(repos, row)
	}
	writeJSON(w, http.StatusOK, repoListJSON{
		Search:   search,
		Page:     page,
		PageSize: repoPageSize,
		Total:    total,
		Repos:    repos,
	})
}

func (h *HTTPAPI) setPublished(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name      string `json:"name"`
		Published *bool  `json:"published"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
	if err := dec.Decode(&body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid publish request")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeAPIError(w, http.StatusBadRequest, "repository name is required")
		return
	}
	if body.Published == nil {
		writeAPIError(w, http.StatusBadRequest, "published must be true or false")
		return
	}
	n, err := h.q.SetRepositoryPublished(r.Context(), db.SetRepositoryPublishedParams{
		Published: *body.Published,
		Name:      name,
	})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if n == 0 {
		writeAPIError(w, http.StatusNotFound, "not found")
		return
	}
	action := auditActionPublish
	if !*body.Published {
		action = auditActionUnpublish
	}
	audit(h.q, r, action, name)
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "published": *body.Published})
}

// repoBytes is the repository's stored size. The catalog query sums
// blob_metadata, which is empty until a blob is recorded there. Pushed
// blobs still have blob_refs and bytes in the store, so a zero metadata
// sum falls back to those stored lengths.
func (h *HTTPAPI) repoBytes(ctx context.Context, repoID, recorded int64) (int64, error) {
	if recorded > 0 || h.store == nil {
		return recorded, nil
	}
	refs, err := h.q.ListBlobRefsByRepo(ctx, repoID)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, ref := range refs {
		n, err := h.store.Size(ctx, ref.Digest)
		if err != nil {
			if errors.Is(err, storage.ErrBlobNotFound) {
				continue
			}
			return 0, err
		}
		total += n
	}
	return total, nil
}

// pathParam returns a chi path parameter with percent-encoding removed.
// Digests contain a colon, and the client encodes that colon as %3A.
func pathParam(r *http.Request, name string) string {
	raw := chi.URLParam(r, name)
	value, err := url.PathUnescape(raw)
	if err != nil {
		return raw
	}
	return value
}

// catalogMatches reports whether name would survive the catalog LIKE filter.
// Repository names are lowercase, and SQLite LIKE is ASCII-case-insensitive,
// so a lowercased substring compare matches the pattern the query builds.
func catalogMatches(name, search string) bool {
	if search == "" {
		return true
	}
	return strings.Contains(strings.ToLower(name), strings.ToLower(search))
}

// latestTag is the repository's newest tag and the manifest behind it. The
// tag query orders by name, so the newest one is chosen here. An untagged
// repository returns empty strings.
func (h *HTTPAPI) latestTag(ctx context.Context, repoID int64) (name, digest string, err error) {
	tags, err := h.q.ListTagsByRepoFromStart(ctx, db.ListTagsByRepoFromStartParams{
		RepoID: repoID,
		Limit:  maxTagRows,
	})
	if err != nil {
		return "", "", err
	}
	best := -1
	for i := range tags {
		if best < 0 || tags[i].UpdatedAt > tags[best].UpdatedAt ||
			(tags[i].UpdatedAt == tags[best].UpdatedAt && tags[i].Name < tags[best].Name) {
			best = i
		}
	}
	if best < 0 {
		return "", "", nil
	}
	return tags[best].Name, tags[best].ManifestDigest, nil
}

func (h *HTTPAPI) attachPull(item *repoJSON, tag string) {
	if h.pullHost == "" || tag == "" {
		return
	}
	item.LatestTag = tag
	item.PullHost = h.pullHost
}

// maxTagRows bounds one repository's tag list. The registry /tags/list
// endpoint is the bulk surface.
const maxTagRows = 500

type tagJSON struct {
	Name      string    `json:"name"`
	Digest    string    `json:"digest"`
	Size      int64     `json:"size"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type tagListJSON struct {
	Name     string    `json:"name"`
	PullHost string    `json:"pullHost,omitempty"`
	Tags     []tagJSON `json:"tags"`
}

func (h *HTTPAPI) listTags(w http.ResponseWriter, r *http.Request) {
	name := RepoPathParam(r)
	if name == "" {
		writeAPIError(w, http.StatusNotFound, "not found")
		return
	}
	ctx := r.Context()
	repo, err := h.q.GetRepositoryByName(ctx, name)
	if errors.Is(err, sql.ErrNoRows) {
		writeAPIError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	rows, err := h.q.ListTagsByRepoFromStart(ctx, db.ListTagsByRepoFromStartParams{
		RepoID: repo.ID,
		Limit:  maxTagRows,
	})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	tags := make([]tagJSON, 0, len(rows))
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
					writeAPIError(w, http.StatusInternalServerError, "internal server error")
					return
				}
				size = -1
			} else {
				size = manifestContentSize(m.Payload, m.Size)
			}
			sizes[row.ManifestDigest] = size
		}
		tags = append(tags, tagJSON{
			Name:      row.Name,
			Digest:    row.ManifestDigest,
			Size:      size,
			UpdatedAt: time.Unix(row.UpdatedAt, 0).UTC(),
		})
	}
	payload := tagListJSON{Name: name, Tags: tags}
	if h.pullHost != "" {
		payload.PullHost = h.pullHost
	}
	writeJSON(w, http.StatusOK, payload)
}

type layerJSON struct {
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	MediaType string `json:"mediaType,omitempty"`
}

type imageLayerJSON struct {
	Command string `json:"command"`
	Size    int64  `json:"size"`
	Digest  string `json:"digest,omitempty"`
}

type configJSON struct {
	Cmd          []string `json:"cmd,omitempty"`
	Entrypoint   []string `json:"entrypoint,omitempty"`
	Env          []string `json:"env,omitempty"`
	WorkingDir   string   `json:"workingDir,omitempty"`
	ExposedPorts []string `json:"exposedPorts,omitempty"`
	User         string   `json:"user,omitempty"`
}

type manifestJSON struct {
	Repo         string           `json:"repo"`
	Digest       string           `json:"digest"`
	MediaType    string           `json:"mediaType"`
	Size         int64            `json:"size"`
	ConfigDigest string           `json:"configDigest,omitempty"`
	Layers       []layerJSON      `json:"layers"`
	ImageLayers  []imageLayerJSON `json:"imageLayers"`
	Config       *configJSON      `json:"config,omitempty"`
}

func (h *HTTPAPI) showManifest(w http.ResponseWriter, r *http.Request) {
	name := RepoPathParam(r)
	digest := pathParam(r, "digest")
	if name == "" || digest == "" {
		writeAPIError(w, http.StatusNotFound, "not found")
		return
	}
	ctx := r.Context()
	repo, err := h.q.GetRepositoryByName(ctx, name)
	if errors.Is(err, sql.ErrNoRows) {
		writeAPIError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	m, err := h.q.GetManifest(ctx, db.GetManifestParams{RepoID: repo.ID, Digest: digest})
	if errors.Is(err, sql.ErrNoRows) {
		writeAPIError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if !json.Valid(m.Payload) {
		writeAPIError(w, http.StatusUnprocessableEntity, "manifest payload is not valid JSON")
		return
	}
	d := BuildManifestDisplay(ctx, m, h.store)
	layers := make([]layerJSON, 0, len(d.Layers))
	for _, layer := range d.Layers {
		layers = append(layers, layerJSON{Digest: layer.Digest, Size: layer.Size, MediaType: layer.MediaType})
	}
	history := make([]imageLayerJSON, 0, len(d.ImageLayers))
	for _, layer := range d.ImageLayers {
		history = append(history, imageLayerJSON{Command: layer.Command, Size: layer.Size, Digest: layer.Digest})
	}
	out := manifestJSON{
		Repo:         name,
		Digest:       d.Digest,
		MediaType:    d.MediaType,
		Size:         d.Size,
		ConfigDigest: d.ConfigDigest,
		Layers:       layers,
		ImageLayers:  history,
	}
	if c := d.ContainerConfig; c != nil {
		out.Config = &configJSON{
			Cmd:          c.Cmd,
			Entrypoint:   c.Entrypoint,
			Env:          c.Env,
			WorkingDir:   c.WorkingDir,
			ExposedPorts: c.ExposedPorts,
			User:         c.User,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *HTTPAPI) deleteTag(w http.ResponseWriter, r *http.Request) {
	name, tag := RepoPathParam(r), pathParam(r, "tag")
	code := h.deleter.removeTag(r, name, tag)
	if code != http.StatusNoContent {
		writeAPIError(w, code, deleteMessage(code))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTPAPI) deleteManifest(w http.ResponseWriter, r *http.Request) {
	name, digest := RepoPathParam(r), pathParam(r, "digest")
	code := h.deleter.removeManifest(r, name, digest)
	if code != http.StatusNoContent {
		writeAPIError(w, code, deleteMessage(code))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func deleteMessage(code int) string {
	if code == http.StatusNotFound {
		return "not found"
	}
	return "internal server error"
}

type retagBody struct {
	TargetRepo string `json:"targetRepo"`
	TargetTag  string `json:"targetTag"`
}

type retagJSON struct {
	Repo string `json:"repo"`
	Tag  string `json:"tag"`
}

func (h *HTTPAPI) retag(w http.ResponseWriter, r *http.Request) {
	name, tag := RepoPathParam(r), pathParam(r, "tag")
	r.Body = http.MaxBytesReader(w, r.Body, maxRetagFormBytes)
	var body retagBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid retag body")
		return
	}
	dst, status, msg := h.retagger.copyTag(r, name, tag, strings.TrimSpace(body.TargetRepo), strings.TrimSpace(body.TargetTag))
	if status != http.StatusOK {
		if msg == "" {
			msg = deleteMessage(status)
		}
		writeAPIError(w, status, msg)
		return
	}
	writeJSON(w, http.StatusOK, retagJSON{Repo: dst, Tag: strings.TrimSpace(body.TargetTag)})
}

type gcStatsJSON struct {
	TotalBlobs        int64      `json:"totalBlobs"`
	ReferencedBlobs   int64      `json:"referencedBlobs"`
	UnreferencedBlobs int64      `json:"unreferencedBlobs"`
	UnreferencedBytes int64      `json:"unreferencedBytes"`
	IntervalSeconds   int64      `json:"intervalSeconds"`
	LastRunAt         *time.Time `json:"lastRunAt,omitempty"`
}

type gcBlobJSON struct {
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

type gcResultJSON struct {
	Stats  gcStatsJSON  `json:"stats"`
	Blobs  []gcBlobJSON `json:"blobs"`
	Bytes  int64        `json:"bytes"`
	Failed int          `json:"failed"`
	Ran    bool         `json:"ran"`
}

func (h *HTTPAPI) gcStats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.gc.runner.Stats(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, h.statsJSON(stats))
}

func (h *HTTPAPI) gcDryRun(w http.ResponseWriter, r *http.Request) {
	blobs, err := h.gc.runner.DryRun(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	stats, err := h.gc.runner.Stats(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, gcResultJSON{
		Stats: h.statsJSON(stats),
		Blobs: blobsJSON(blobs),
		Bytes: gcBytes(blobs),
		Ran:   false,
	})
}

func (h *HTTPAPI) gcRun(w http.ResponseWriter, r *http.Request) {
	var result gc.Result
	ran, err := gc.Collect(r.Context(), h.gc.lock, func(ctx context.Context) error {
		var runErr error
		result, runErr = h.gc.runner.Run(ctx)
		return runErr
	})
	if !ran && err == nil {
		writeAPIError(w, http.StatusConflict, "a collection is already running")
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	h.gcLast.Store(time.Now().Unix())
	stats, err := h.gc.runner.Stats(r.Context())
	if err != nil {
		stats = gc.Stats{}
	}
	writeJSON(w, http.StatusOK, gcResultJSON{
		Stats:  h.statsJSON(stats),
		Blobs:  blobsJSON(result.Deleted),
		Bytes:  result.BytesFreed,
		Failed: result.Failed,
		Ran:    true,
	})
}

func (h *HTTPAPI) statsJSON(s gc.Stats) gcStatsJSON {
	out := gcStatsJSON{
		TotalBlobs:        s.TotalBlobs,
		ReferencedBlobs:   s.ReferencedBlobs,
		UnreferencedBlobs: s.UnreferencedBlobs,
		UnreferencedBytes: s.UnreferencedBytes,
		IntervalSeconds:   int64(h.gcEvery.Seconds()),
	}
	if unix := h.gcLast.Load(); unix > 0 {
		at := time.Unix(unix, 0).UTC()
		out.LastRunAt = &at
	}
	return out
}

func blobsJSON(blobs []gc.Blob) []gcBlobJSON {
	out := make([]gcBlobJSON, 0, len(blobs))
	for _, b := range blobs {
		out = append(out, gcBlobJSON{Digest: b.Digest, Size: b.Size})
	}
	return out
}

// manifestContentSize is the image size a tag row shows: config plus layers,
// or the child manifests of an index. The manifest document's own byte
// length is only the fallback when the payload has no descriptors.
func manifestContentSize(payload []byte, fallback int64) int64 {
	var p manifestPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fallback
	}
	entries := p.Layers
	if len(entries) == 0 {
		entries = p.Manifests
	}
	var n int64
	if p.Config != nil {
		n += p.Config.Size
	}
	for _, e := range entries {
		n += e.Size
	}
	if n == 0 {
		return fallback
	}
	return n
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeAPIError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
