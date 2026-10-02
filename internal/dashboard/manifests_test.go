package dashboard_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/user/better-docker-registry/internal/dashboard"
	"github.com/user/better-docker-registry/internal/db"
)

// manifestTestRouter wires the dashboard routes over a fresh SQLite database.
func manifestTestRouter(t *testing.T) (http.Handler, db.Querier) {
	t.Helper()
	sqlDB, err := db.OpenAndMigrate(t.Context(), filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	q := db.New(sqlDB)

	inner := chi.NewRouter()
	dashboard.NewTagsHandler(q).Mount(inner)
	dashboard.NewManifestsHandler(q).Mount(inner)

	r := chi.NewRouter()
	r.Mount("/dashboard/repos", dashboard.CollapseRepoName(inner))
	return r, q
}

// manifestTestImagePayload is a well-formed Docker image manifest with one
// config and two layers.
const manifestTestImagePayload = `{
	"schemaVersion": 2,
	"mediaType": "application/vnd.docker.distribution.manifest.v2+json",
	"config": {
		"mediaType": "application/vnd.docker.container.image.v1+json",
		"digest": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"size": 1234
	},
	"layers": [
		{
			"mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
			"digest": "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			"size": 1024
		},
		{
			"mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
			"digest": "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			"size": 2048
		}
	]
}`

// manifestTestIndexPayload is a well-formed OCI image index with two entries.
const manifestTestIndexPayload = `{
	"schemaVersion": 2,
	"mediaType": "application/vnd.oci.image.index.v1+json",
	"manifests": [
		{
			"mediaType": "application/vnd.oci.image.manifest.v1+json",
			"digest": "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
			"size": 512
		},
		{
			"mediaType": "application/vnd.oci.image.manifest.v1+json",
			"digest": "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
			"size": 768
		}
	]
}`

// manifestTestSeed creates a repo with one manifest of the given payload and
// one tag pointing at it. Returns the manifest digest.
func manifestTestSeed(t *testing.T, q db.Querier, repoName, payload, mediaType string) string {
	t.Helper()
	ctx := t.Context()
	repo, err := q.CreateRepository(ctx, repoName)
	if err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}
	sum := sha256Hex(payload)
	digest := "sha256:" + sum
	if err := q.UpsertManifest(ctx, db.UpsertManifestParams{
		RepoID:    repo.ID,
		Digest:    digest,
		MediaType: mediaType,
		Payload:   []byte(payload),
		Size:      int64(len(payload)),
	}); err != nil {
		t.Fatalf("UpsertManifest: %v", err)
	}
	if err := q.SetTag(ctx, db.SetTagParams{
		RepoID:         repo.ID,
		Name:           "latest",
		ManifestDigest: digest,
		UpdatedAt:      time.Now().Unix(),
	}); err != nil {
		t.Fatalf("SetTag: %v", err)
	}
	return digest
}

// manifestTestGET issues a GET and returns the recorded response.
func manifestTestGET(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// sha256Hex returns the hex-encoded SHA-256 of s (without the sha256: prefix).
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// --- Tests ---

// Given: a repo "test" with an image manifest (1 config + 2 layers).
// When:  GET /repos/test/manifests/{digest}.
// Then:  the body shows media type, config digest and 2 layer rows.
func Test_showManifest_when_imageManifest_then_showsLayers(t *testing.T) {
	h, q := manifestTestRouter(t)
	digest := manifestTestSeed(t, q, "test", manifestTestImagePayload,
		"application/vnd.docker.distribution.manifest.v2+json")

	rec := manifestTestGET(t, h, "/dashboard/repos/test/manifests/"+digest)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body)
	}
	body := rec.Body.String()

	// Media type
	if !strings.Contains(body, "application/vnd.docker.distribution.manifest.v2+json") {
		t.Error("body missing media type")
	}
	// Config digest
	if !strings.Contains(body, "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") {
		t.Error("body missing config digest")
	}
	// Layer rows
	if got := strings.Count(body, `data-testid="layer-row"`); got != 2 {
		t.Fatalf("layer-row count = %d, want 2", got)
	}
	// Layer digests
	for _, d := range []string{
		"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
	} {
		if !strings.Contains(body, d) {
			t.Errorf("body missing layer digest %s", d)
		}
	}
	// No raw JSON displayed
	if strings.Contains(body, `"schemaVersion"`) {
		t.Error("body should not contain raw JSON payload")
	}
}

// Given: a repo "test" with an OCI index manifest (2 child entries).
// When:  GET /repos/test/manifests/{digest}.
// Then:  the body shows 2 layer rows (child manifests) with their digests.
func Test_showManifest_when_indexManifest_then_showsChildManifests(t *testing.T) {
	h, q := manifestTestRouter(t)
	digest := manifestTestSeed(t, q, "test", manifestTestIndexPayload,
		"application/vnd.oci.image.index.v1+json")

	rec := manifestTestGET(t, h, "/dashboard/repos/test/manifests/"+digest)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body)
	}
	body := rec.Body.String()
	if got := strings.Count(body, `data-testid="layer-row"`); got != 2 {
		t.Fatalf("layer-row count = %d, want 2", got)
	}
	for _, d := range []string{
		"sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		"sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
	} {
		if !strings.Contains(body, d) {
			t.Errorf("body missing child manifest digest %s", d)
		}
	}
	// Index has no config digest
	if strings.Contains(body, "Config Digest") {
		t.Error("index manifest should not show config digest")
	}
}

// Given: a repo "test" with a manifest whose payload is not valid JSON.
// When:  GET /repos/test/manifests/{digest}.
// Then:  the response shows an error message (not raw JSON, not a 500).
func Test_showManifest_when_invalidJSON_then_errorDisplayed(t *testing.T) {
	h, q := manifestTestRouter(t)
	ctx := t.Context()
	repo, err := q.CreateRepository(ctx, "test")
	if err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}
	digest := "sha256:" + strings.Repeat("f", 64)
	if err := q.UpsertManifest(ctx, db.UpsertManifestParams{
		RepoID:    repo.ID,
		Digest:    digest,
		MediaType: "application/vnd.docker.distribution.manifest.v2+json",
		Payload:   []byte(`{invalid json`),
		Size:      13,
	}); err != nil {
		t.Fatalf("UpsertManifest: %v", err)
	}

	rec := manifestTestGET(t, h, "/dashboard/repos/test/manifests/"+digest)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-testid="manifest-error"`) {
		t.Error("body missing manifest-error testid")
	}
	if !strings.Contains(body, "not valid JSON") {
		t.Error("body missing error message")
	}
	if strings.Contains(body, `{invalid json`) {
		t.Error("body should not show raw payload")
	}
}

// Given: a manifest that does not exist.
// When:  GET /repos/test/manifests/sha256:0000...0000.
// Then:  the response is 404.
func Test_showManifest_when_manifestNotFound_then_404(t *testing.T) {
	h, q := manifestTestRouter(t)
	if _, err := q.CreateRepository(t.Context(), "test"); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}
	digest := "sha256:" + strings.Repeat("0", 64)
	rec := manifestTestGET(t, h, "/dashboard/repos/test/manifests/"+digest)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// Given: no repo named "missing".
// When:  GET /repos/missing/manifests/{digest}.
// Then:  the response is 404.
func Test_showManifest_when_repoNotFound_then_404(t *testing.T) {
	h, _ := manifestTestRouter(t)
	digest := "sha256:" + strings.Repeat("a", 64)
	rec := manifestTestGET(t, h, "/dashboard/repos/missing/manifests/"+digest)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// Given: a repo with an image manifest.
// When:  GET with HX-Request: true.
// Then:  the response is a fragment (no <html>), containing the layer table.
func Test_showManifest_when_htmxRequest_then_fragment(t *testing.T) {
	h, q := manifestTestRouter(t)
	digest := manifestTestSeed(t, q, "test", manifestTestImagePayload,
		"application/vnd.docker.distribution.manifest.v2+json")

	req := httptest.NewRequest(http.MethodGet, "/dashboard/repos/test/manifests/"+digest, nil)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if strings.Contains(body, "<html") {
		t.Error("HTMX fragment should not contain <html>")
	}
	if !strings.Contains(body, `data-testid="layer-row"`) {
		t.Error("fragment missing layer rows")
	}
}

// buildManifestDisplay: image manifest produces config digest + layer list.
func Test_buildManifestDisplay_when_imageManifest_then_configAndLayers(t *testing.T) {
	m := db.Manifest{
		Digest:    "sha256:" + strings.Repeat("a", 64),
		MediaType: "application/vnd.docker.distribution.manifest.v2+json",
		Payload:   []byte(manifestTestImagePayload),
		Size:      int64(len(manifestTestImagePayload)),
	}
	d := dashboard.BuildManifestDisplay(m)
	if d.ConfigDigest != "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Errorf("ConfigDigest = %q", d.ConfigDigest)
	}
	if len(d.Layers) != 2 {
		t.Fatalf("Layers len = %d, want 2", len(d.Layers))
	}
	if d.Layers[0].Size != 1024 {
		t.Errorf("Layers[0].Size = %d, want 1024", d.Layers[0].Size)
	}
	if d.Layers[1].MediaType != "application/vnd.docker.image.rootfs.diff.tar.gzip" {
		t.Errorf("Layers[1].MediaType = %q", d.Layers[1].MediaType)
	}
}

// buildManifestDisplay: index manifest produces child manifest list, no config.
func Test_buildManifestDisplay_when_indexManifest_then_childManifests(t *testing.T) {
	m := db.Manifest{
		Digest:    "sha256:" + strings.Repeat("b", 64),
		MediaType: "application/vnd.oci.image.index.v1+json",
		Payload:   []byte(manifestTestIndexPayload),
		Size:      int64(len(manifestTestIndexPayload)),
	}
	d := dashboard.BuildManifestDisplay(m)
	if d.ConfigDigest != "" {
		t.Errorf("ConfigDigest = %q, want empty for index", d.ConfigDigest)
	}
	if len(d.Layers) != 2 {
		t.Fatalf("Layers len = %d, want 2", len(d.Layers))
	}
	if d.Layers[0].Digest != "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd" {
		t.Errorf("Layers[0].Digest = %q", d.Layers[0].Digest)
	}
}

// manifestPayload must unmarshal the fields the detail view shows.
func Test_manifestPayload_when_unmarshal_then_fieldsExtracted(t *testing.T) {
	var p struct {
		Config *struct {
			Digest string `json:"digest"`
		} `json:"config"`
		Layers []struct {
			Digest    string `json:"digest"`
			Size      int64  `json:"size"`
			MediaType string `json:"mediaType"`
		} `json:"layers"`
	}
	if err := json.Unmarshal([]byte(manifestTestImagePayload), &p); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if p.Config == nil || p.Config.Digest == "" {
		t.Error("config digest not extracted")
	}
	if len(p.Layers) != 2 {
		t.Errorf("layers len = %d, want 2", len(p.Layers))
	}
}
