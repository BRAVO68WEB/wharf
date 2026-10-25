package dashboard_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/user/better-docker-registry/internal/dashboard"
	"github.com/user/better-docker-registry/internal/db"
	"github.com/user/better-docker-registry/internal/gc"
)

func apiTestRouter(t *testing.T) (http.Handler, db.Querier, *gcTestStore) {
	t.Helper()
	sqlDB, err := db.OpenAndMigrate(t.Context(), filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	q := db.New(sqlDB)
	store := newGCTestStore()
	r := chi.NewRouter()
	api := dashboard.NewHTTPAPI(q, store, gc.NewLock())
	api.MountPublic(r)
	api.Mount(r)
	return r, q, store
}

func TestAPI_pullHost_omittedUntilConfigured(t *testing.T) {
	h, q, _ := apiTestRouter(t)
	apiSeed(t, q, "app", "latest", "sha256:"+strings.Repeat("a", 64), 1_700_000_000, 10)
	body := apiGetJSON(t, h, "/api/repo/app")
	if _, ok := body["pullHost"]; ok {
		t.Fatalf("pullHost present without configuration: %#v", body["pullHost"])
	}

	r := chi.NewRouter()
	sqlDB, err := db.OpenAndMigrate(t.Context(), filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	q = db.New(sqlDB)
	api := dashboard.NewHTTPAPI(q, newGCTestStore(), gc.NewLock())
	api.SetPullHost("registry.example.com:5000")
	api.Mount(r)
	api.MountPublic(r)
	apiSeed(t, q, "app", "latest", "sha256:"+strings.Repeat("c", 64), 1_700_000_000, 10)
	if _, err := q.SetRepositoryPublished(t.Context(), db.SetRepositoryPublishedParams{Published: true, Name: "app"}); err != nil {
		t.Fatal(err)
	}

	tags := apiGetJSON(t, r, "/api/repo/app")
	if tags["pullHost"] != "registry.example.com:5000" {
		t.Fatalf("tag pullHost = %#v", tags["pullHost"])
	}
	repos := apiGetJSON(t, r, "/api/public/repos")
	row := repos["repos"].([]any)[0].(map[string]any)
	if row["pullHost"] != "registry.example.com:5000" || row["latestTag"] != "latest" {
		t.Fatalf("public pull fields = %#v", row)
	}
}

func apiSeed(t *testing.T, q db.Querier, name, tag, digest string, updated, blobSize int64) {
	t.Helper()
	repo, err := q.CreateRepository(t.Context(), name)
	if err != nil {
		t.Fatalf("CreateRepository %s: %v", name, err)
	}
	payload := fmt.Sprintf(`{"schemaVersion":2,"layers":[{"digest":%q,"size":%d}]}`, "sha256:"+strings.Repeat("b", 64), blobSize)
	if err := q.UpsertManifest(t.Context(), db.UpsertManifestParams{
		RepoID:    repo.ID,
		Digest:    digest,
		MediaType: "application/vnd.docker.distribution.manifest.v2+json",
		Payload:   []byte(payload),
		Size:      12,
	}); err != nil {
		t.Fatalf("UpsertManifest %s: %v", name, err)
	}
	if err := q.SetTag(t.Context(), db.SetTagParams{
		RepoID:         repo.ID,
		Name:           tag,
		ManifestDigest: digest,
		UpdatedAt:      updated,
	}); err != nil {
		t.Fatalf("SetTag %s: %v", name, err)
	}
	blob := "sha256:" + fmt.Sprintf("%064x", blobSize+int64(len(name)))
	if err := q.UpsertBlobMetadata(t.Context(), db.UpsertBlobMetadataParams{
		Digest: blob, Size: blobSize, MediaType: "application/octet-stream",
	}); err != nil {
		t.Fatalf("UpsertBlobMetadata %s: %v", name, err)
	}
	if err := q.AddBlobRef(t.Context(), db.AddBlobRefParams{Digest: blob, RepoID: repo.ID}); err != nil {
		t.Fatalf("AddBlobRef %s: %v", name, err)
	}
}

func TestAPI_listRepos_mountsNewestAndFiltersIndex(t *testing.T) {
	h, q, _ := apiTestRouter(t)
	oldDigest := "sha256:" + strings.Repeat("1", 64)
	newDigest := "sha256:" + strings.Repeat("2", 64)
	apiSeed(t, q, "old-app", "latest", oldDigest, 1_700_000_000, 10)
	apiSeed(t, q, "new-app", "latest", newDigest, 1_800_000_000, 30)

	body := apiGetJSON(t, h, "/api/repos")
	if body["total"].(float64) != 1 {
		t.Fatalf("total = %v, want 1", body["total"])
	}
	mounted := body["mounted"].(map[string]any)
	if mounted["name"] != "new-app" || mounted["digest"] != newDigest || mounted["sizeBytes"].(float64) != 30 {
		t.Fatalf("mounted = %#v", mounted)
	}
	repos := body["repos"].([]any)
	if len(repos) != 1 || repos[0].(map[string]any)["name"] != "old-app" {
		t.Fatalf("repos = %#v", repos)
	}

	filtered := apiGetJSON(t, h, "/api/repos?search=old")
	if filtered["mounted"].(map[string]any)["name"] != "new-app" {
		t.Fatalf("search moved the mount: %#v", filtered["mounted"])
	}
	if filtered["total"].(float64) != 1 {
		t.Fatalf("filtered total = %v", filtered["total"])
	}
	only := filtered["repos"].([]any)
	if len(only) != 1 || only[0].(map[string]any)["name"] != "old-app" {
		t.Fatalf("filtered repos = %#v", only)
	}
}

func TestAPI_tagsAndManifest_keepFullHistory(t *testing.T) {
	h, q, store := apiTestRouter(t)
	const tail = "this-tail-must-remain"
	config := `{
		"history": [
			{"created_by": "/bin/sh -c #(nop) ADD file:abc in /"},
			{"created_by": "/bin/sh -c #(nop) ENV PATH=/usr/bin", "empty_layer": true},
			{"created_by": "/bin/sh -c npm i && ` + tail + `"}
		]
	}`
	configDigest := manifestTestPutBlob(t, store, config)
	layerA := "sha256:" + strings.Repeat("a", 64)
	layerB := "sha256:" + strings.Repeat("b", 64)
	payload := fmt.Sprintf(`{
		"schemaVersion": 2,
		"config": {"digest": %q, "size": %d, "mediaType": "application/vnd.docker.container.image.v1+json"},
		"layers": [
			{"digest": %q, "size": 1024, "mediaType": "application/vnd.oci.image.layer.v1.tar"},
			{"digest": %q, "size": 2048, "mediaType": "application/vnd.oci.image.layer.v1.tar"}
		]
	}`, configDigest, len(config), layerA, layerB)
	digest := "sha256:" + strings.Repeat("c", 64)
	repo, err := q.CreateRepository(t.Context(), "library/nginx")
	if err != nil {
		t.Fatal(err)
	}
	if err := q.UpsertManifest(t.Context(), db.UpsertManifestParams{
		RepoID: repo.ID, Digest: digest, MediaType: "application/vnd.oci.image.manifest.v1+json",
		Payload: []byte(payload), Size: int64(len(payload)),
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.SetTag(t.Context(), db.SetTagParams{
		RepoID: repo.ID, Name: "latest", ManifestDigest: digest, UpdatedAt: 1_800_000_000,
	}); err != nil {
		t.Fatal(err)
	}

	tags := apiGetJSON(t, h, "/api/repo/library/nginx")
	if tags["name"] != "library/nginx" {
		t.Fatalf("name = %v", tags["name"])
	}
	tagRows := tags["tags"].([]any)
	if len(tagRows) != 1 || tagRows[0].(map[string]any)["size"].(float64) != float64(len(config)+1024+2048) {
		t.Fatalf("tags = %#v", tagRows)
	}

	manifest := apiGetJSON(t, h, "/api/repo/library/nginx/manifests/"+digest)
	layers := manifest["imageLayers"].([]any)
	if len(layers) != 3 {
		t.Fatalf("imageLayers = %#v", layers)
	}
	if layers[0].(map[string]any)["command"] != "ADD file:abc in /" {
		t.Fatalf("layer 0 = %#v", layers[0])
	}
	if got := layers[2].(map[string]any)["command"].(string); !strings.Contains(got, tail) || strings.Contains(got, "…") {
		t.Fatalf("layer command = %q", got)
	}
	if layers[2].(map[string]any)["size"].(float64) != 2048 {
		t.Fatalf("layer 2 size = %#v", layers[2])
	}

	encoded := apiGetJSON(t, h, "/api/repo/library/nginx/manifests/"+url.PathEscape(digest))
	if encoded["digest"] != digest {
		t.Fatalf("encoded digest = %v", encoded["digest"])
	}
}

func TestAPI_repoSize_whenMetadataMissing_usesStoredBlobs(t *testing.T) {
	h, q, store := apiTestRouter(t)
	blob := manifestTestPutBlob(t, store, "stored-bytes")
	repo, err := q.CreateRepository(t.Context(), "sized")
	if err != nil {
		t.Fatal(err)
	}
	manifest := "sha256:" + strings.Repeat("e", 64)
	if err := q.UpsertManifest(t.Context(), db.UpsertManifestParams{
		RepoID: repo.ID, Digest: manifest, MediaType: "application/vnd.oci.image.manifest.v1+json",
		Payload: []byte(`{"schemaVersion":2}`), Size: 20,
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.SetTag(t.Context(), db.SetTagParams{
		RepoID: repo.ID, Name: "latest", ManifestDigest: manifest, UpdatedAt: 1_800_000_000,
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.AddBlobRef(t.Context(), db.AddBlobRefParams{Digest: blob, RepoID: repo.ID}); err != nil {
		t.Fatal(err)
	}

	body := apiGetJSON(t, h, "/api/repos")
	mounted := body["mounted"].(map[string]any)
	if mounted["name"] != "sized" || mounted["sizeBytes"].(float64) != float64(len("stored-bytes")) {
		t.Fatalf("mounted = %#v", mounted)
	}
}

func TestAPI_deleteAndRetag(t *testing.T) {
	h, q, _ := apiTestRouter(t)
	digest := "sha256:" + strings.Repeat("d", 64)
	apiSeed(t, q, "app", "latest", digest, 1_800_000_000, 4)

	rec := apiDo(t, h, http.MethodDelete, "/api/repo/app/tags/latest", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d body=%s", rec.Code, rec.Body.String())
	}
	missing := apiDo(t, h, http.MethodDelete, "/api/repo/app/tags/latest", "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("second delete = %d", missing.Code)
	}

	if err := q.SetTag(t.Context(), db.SetTagParams{
		RepoID: mustRepoID(t, q, "app"), Name: "latest", ManifestDigest: digest, UpdatedAt: 1_800_000_100,
	}); err != nil {
		t.Fatal(err)
	}
	retagged := apiDo(t, h, http.MethodPost, "/api/repo/app/tags/latest/retag", `{"targetRepo":"other/app","targetTag":"stable"}`)
	if retagged.Code != http.StatusOK {
		t.Fatalf("retag = %d body=%s", retagged.Code, retagged.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(retagged.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["repo"] != "other/app" || got["tag"] != "stable" {
		t.Fatalf("retag body = %#v", got)
	}
}

func TestAPI_gcStats(t *testing.T) {
	h, _, _ := apiTestRouter(t)
	rec := apiDo(t, h, http.MethodGet, "/api/gc", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("stats = %d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "lastRunAt") || strings.Contains(rec.Body.String(), "0001-01-01") {
		t.Fatalf("idle stats leaked a last run: %s", rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["totalBlobs"].(float64) != 0 || body["unreferencedBytes"].(float64) != 0 {
		t.Fatalf("stats = %#v", body)
	}
	if body["intervalSeconds"].(float64) != 0 {
		t.Fatalf("interval = %#v", body["intervalSeconds"])
	}
	dry := apiDo(t, h, http.MethodPost, "/api/gc/dry-run", "")
	if dry.Code != http.StatusOK {
		t.Fatalf("dry-run = %d body=%s", dry.Code, dry.Body.String())
	}
	if strings.Contains(dry.Body.String(), "lastRunAt") {
		t.Fatalf("dry-run leaked a last run: %s", dry.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(dry.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["ran"] != false {
		t.Fatalf("ran = %v", result["ran"])
	}
	if _, ok := result["blobs"].([]any); !ok {
		t.Fatalf("blobs = %#v", result["blobs"])
	}

	run := apiDo(t, h, http.MethodPost, "/api/gc/run", "")
	if run.Code != http.StatusOK {
		t.Fatalf("run = %d body=%s", run.Code, run.Body.String())
	}
	var ran map[string]any
	if err := json.Unmarshal(run.Body.Bytes(), &ran); err != nil {
		t.Fatal(err)
	}
	stats, _ := ran["stats"].(map[string]any)
	at, _ := stats["lastRunAt"].(string)
	when, err := time.Parse(time.RFC3339, at)
	if err != nil || when.Year() < 2020 || time.Since(when) > time.Minute || time.Until(when) > time.Minute {
		t.Fatalf("lastRunAt = %q", at)
	}
	again := apiGetJSON(t, h, "/api/gc")
	if again["lastRunAt"] != at {
		t.Fatalf("stats after run = %#v", again["lastRunAt"])
	}
}

func TestAPI_gcStats_configuredInterval(t *testing.T) {
	sqlDB, err := db.OpenAndMigrate(t.Context(), filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	api := dashboard.NewHTTPAPI(db.New(sqlDB), newGCTestStore(), gc.NewLock())
	api.SetGCInterval(90 * time.Minute)
	r := chi.NewRouter()
	api.Mount(r)
	body := apiGetJSON(t, r, "/api/gc")
	if body["intervalSeconds"].(float64) != 5400 {
		t.Fatalf("interval = %#v", body["intervalSeconds"])
	}
	if _, ok := body["lastRunAt"]; ok {
		t.Fatalf("lastRunAt = %#v", body["lastRunAt"])
	}
}

func TestAPI_publicRepos_listsOnlyPublished(t *testing.T) {
	h, q, _ := apiTestRouter(t)
	apiSeed(t, q, "private-app", "latest", "sha256:"+strings.Repeat("a", 64), 1_700_000_000, 10)
	apiSeed(t, q, "public-app", "latest", "sha256:"+strings.Repeat("b", 64), 1_800_000_000, 20)
	n, err := q.SetRepositoryPublished(t.Context(), db.SetRepositoryPublishedParams{Published: true, Name: "public-app"})
	if err != nil || n != 1 {
		t.Fatalf("publish = %d, %v", n, err)
	}

	body := apiGetJSON(t, h, "/api/public/repos")
	if body["mounted"] != nil {
		t.Fatalf("public list pinned a repo: %#v", body["mounted"])
	}
	repos := body["repos"].([]any)
	if len(repos) != 1 || repos[0].(map[string]any)["name"] != "public-app" || repos[0].(map[string]any)["published"] != true {
		t.Fatalf("public repos = %#v", repos)
	}
	filtered := apiGetJSON(t, h, "/api/public/repos?search=private")
	if filtered["total"].(float64) != 0 || len(filtered["repos"].([]any)) != 0 {
		t.Fatalf("search leaked a private repo: %#v", filtered)
	}

	missing := apiDo(t, h, http.MethodPost, "/api/repos/publish", `{"name":"missing","published":true}`)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing publish = %d", missing.Code)
	}
	bad := apiDo(t, h, http.MethodPost, "/api/repos/publish", `{"name":"public-app"}`)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("publish without flag = %d", bad.Code)
	}
	off := apiDo(t, h, http.MethodPost, "/api/repos/publish", `{"name":"public-app","published":false}`)
	if off.Code != http.StatusOK {
		t.Fatalf("unpublish = %d body=%s", off.Code, off.Body.String())
	}
	after := apiGetJSON(t, h, "/api/public/repos")
	if after["total"].(float64) != 0 {
		t.Fatalf("unpublished repo still public: %#v", after)
	}
}

func apiGetJSON(t *testing.T, h http.Handler, path string) map[string]any {
	t.Helper()
	rec := apiDo(t, h, http.MethodGet, path, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d body=%s", path, rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v body=%s", err, rec.Body.String())
	}
	return body
}

func apiDo(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func mustRepoID(t *testing.T, q db.Querier, name string) int64 {
	t.Helper()
	repo, err := q.GetRepositoryByName(t.Context(), name)
	if err != nil {
		t.Fatal(err)
	}
	return repo.ID
}
