package dashboard_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/user/better-docker-registry/internal/dashboard"
	"github.com/user/better-docker-registry/internal/db"
)

// Container config tests: the manifest detail reads the image config blob
// from blob storage and renders its runtime fields as structured data. The
// fixtures and helpers here are shared with the rest of dashboard_test.

// manifestTestConfigBlob is a well-formed Docker image config blob with every
// container config field the detail view shows populated.
const manifestTestConfigBlob = `{
	"architecture": "amd64",
	"os": "linux",
	"config": {
		"User": "1000",
		"Env": [
			"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
			"NGINX_VERSION=1.27.0"
		],
		"Entrypoint": ["/docker-entrypoint.sh"],
		"Cmd": ["nginx", "-g", "daemon off;"],
		"WorkingDir": "/app",
		"ExposedPorts": {"80/tcp": {}, "443/tcp": {}}
	},
	"rootfs": {"type": "layers", "diff_ids": []}
}`

// manifestTestPayloadWithConfig returns an image manifest payload referencing
// the given config blob plus one layer.
func manifestTestPayloadWithConfig(configDigest string, configSize int64) string {
	return fmt.Sprintf(`{
	"schemaVersion": 2,
	"mediaType": "application/vnd.docker.distribution.manifest.v2+json",
	"config": {
		"mediaType": "application/vnd.docker.container.image.v1+json",
		"digest": %q,
		"size": %d
	},
	"layers": [
		{
			"mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
			"digest": "sha256:%s",
			"size": 4096
		}
	]
}`, configDigest, configSize, strings.Repeat("1", 64))
}

// manifestTestPutBlob stores content under its own content address and
// returns the digest.
func manifestTestPutBlob(t *testing.T, store *gcTestStore, content string) string {
	t.Helper()
	digest := "sha256:" + sha256Hex(content)
	if _, err := store.Put(t.Context(), digest, strings.NewReader(content)); err != nil {
		t.Fatalf("Put config blob: %v", err)
	}
	return digest
}

// Given: a repo with an image manifest whose config blob the store holds.
// When:  GET /repos/test/manifests/{digest}.
// Then:  the body shows every container config field, structured and sorted,
//
//	and none of the raw config JSON.
func Test_showManifest_when_configBlobPresent_then_containerConfigShown(t *testing.T) {
	h, q, store := manifestTestRouter(t)
	configDigest := manifestTestPutBlob(t, store, manifestTestConfigBlob)
	digest := manifestTestSeed(t, q, "test",
		manifestTestPayloadWithConfig(configDigest, int64(len(manifestTestConfigBlob))),
		"application/vnd.docker.distribution.manifest.v2+json")

	rec := manifestTestGET(t, h, "/dashboard/repos/test/manifests/"+digest)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body)
	}
	body := rec.Body.String()

	for _, id := range []string{
		`data-testid="container-config"`,
		`data-testid="config-cmd"`,
		`data-testid="config-entrypoint"`,
		`data-testid="config-env"`,
		`data-testid="config-workingdir"`,
		`data-testid="config-exposedports"`,
		`data-testid="config-user"`,
	} {
		if !strings.Contains(body, id) {
			t.Errorf("body missing %s", id)
		}
	}
	for _, want := range []string{
		"nginx",
		"daemon off;",
		"/docker-entrypoint.sh",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"NGINX_VERSION=1.27.0",
		"/app",
		"80/tcp",
		"443/tcp",
		"1000",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing config value %q", want)
		}
	}
	if i, j := strings.Index(body, "443/tcp"), strings.Index(body, "80/tcp"); i < 0 || j < 0 || i > j {
		t.Errorf("exposed ports not sorted: 443/tcp at %d, 80/tcp at %d", i, j)
	}
	for _, raw := range []string{`"architecture"`, `"rootfs"`, `"Cmd"`, `{"80/tcp"`} {
		if strings.Contains(body, raw) {
			t.Errorf("body should not contain raw config JSON %s", raw)
		}
	}
}

// Given: an image manifest whose config blob the store does not hold.
// When:  GET /repos/test/manifests/{digest}.
// Then:  the container config section is absent but the layers still show.
func Test_showManifest_when_configBlobMissing_then_noContainerConfig(t *testing.T) {
	h, q, _ := manifestTestRouter(t)
	digest := manifestTestSeed(t, q, "test", manifestTestImagePayload,
		"application/vnd.docker.distribution.manifest.v2+json")

	rec := manifestTestGET(t, h, "/dashboard/repos/test/manifests/"+digest)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if strings.Contains(body, `data-testid="container-config"`) {
		t.Error("body should omit container config when the blob is not held")
	}
	if got := strings.Count(body, `data-testid="layer-row"`); got != 2 {
		t.Errorf("layer-row count = %d, want 2", got)
	}
}

// Given: an image manifest whose config blob is not valid JSON.
// When:  GET /repos/test/manifests/{digest}.
// Then:  the container config section is absent and the payload is not shown.
func Test_showManifest_when_configBlobMalformed_then_noContainerConfig(t *testing.T) {
	h, q, store := manifestTestRouter(t)
	configDigest := manifestTestPutBlob(t, store, "{not json")
	digest := manifestTestSeed(t, q, "test",
		manifestTestPayloadWithConfig(configDigest, 9),
		"application/vnd.docker.distribution.manifest.v2+json")

	rec := manifestTestGET(t, h, "/dashboard/repos/test/manifests/"+digest)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if strings.Contains(body, `data-testid="container-config"`) {
		t.Error("body should omit container config for a malformed blob")
	}
	if strings.Contains(body, "{not json") {
		t.Error("body should not show raw config blob")
	}
}

// Given: an image manifest with two long layer digests.
// When:  GET /repos/test/manifests/{digest}.
// Then:  each layer row shows a truncated digest and keeps the full one.
func Test_showManifest_when_layers_then_digestTruncated(t *testing.T) {
	h, q, _ := manifestTestRouter(t)
	digest := manifestTestSeed(t, q, "test", manifestTestImagePayload,
		"application/vnd.docker.distribution.manifest.v2+json")

	rec := manifestTestGET(t, h, "/dashboard/repos/test/manifests/"+digest)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	for _, encoded := range []string{strings.Repeat("b", 64), strings.Repeat("c", 64)} {
		short := "sha256:" + encoded[:12] + "…" + encoded[len(encoded)-12:]
		if !strings.Contains(body, short) {
			t.Errorf("body missing truncated digest %q", short)
		}
		if !strings.Contains(body, "sha256:"+encoded) {
			t.Errorf("body missing full digest sha256:%s", encoded)
		}
	}
}

// buildManifestDisplay: a held config blob is parsed into the container config.
func Test_buildManifestDisplay_when_configBlob_then_containerConfigParsed(t *testing.T) {
	store := newGCTestStore()
	configDigest := manifestTestPutBlob(t, store, manifestTestConfigBlob)
	m := db.Manifest{
		Digest:    "sha256:" + strings.Repeat("c", 64),
		MediaType: "application/vnd.docker.distribution.manifest.v2+json",
		Payload:   []byte(manifestTestPayloadWithConfig(configDigest, int64(len(manifestTestConfigBlob)))),
	}
	c := dashboard.BuildManifestDisplay(t.Context(), m, store).ContainerConfig
	if c == nil {
		t.Fatal("ContainerConfig = nil, want parsed config")
	}
	if got, want := c.Cmd, []string{"nginx", "-g", "daemon off;"}; !slices.Equal(got, want) {
		t.Errorf("Cmd = %q, want %q", got, want)
	}
	if got, want := c.Entrypoint, []string{"/docker-entrypoint.sh"}; !slices.Equal(got, want) {
		t.Errorf("Entrypoint = %q, want %q", got, want)
	}
	if got, want := c.Env, []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"NGINX_VERSION=1.27.0",
	}; !slices.Equal(got, want) {
		t.Errorf("Env = %q, want %q", got, want)
	}
	if c.WorkingDir != "/app" {
		t.Errorf("WorkingDir = %q, want %q", c.WorkingDir, "/app")
	}
	if c.User != "1000" {
		t.Errorf("User = %q, want %q", c.User, "1000")
	}
	if got, want := c.ExposedPorts, []string{"443/tcp", "80/tcp"}; !slices.Equal(got, want) {
		t.Errorf("ExposedPorts = %q, want %q", got, want)
	}
}

// Given: an image whose config history has a long ADD digest, an empty ENV
// step, and a long RUN script, plus two layer descriptors.
// When:  GET the manifest detail.
// Then:  every command is rendered whole (no ellipsis), the empty step is
//
//	0 B, and each real step keeps its layer size, in history order.
func Test_showManifest_when_history_then_fullLayerCommands(t *testing.T) {
	fileID := strings.Repeat("ab", 32)
	add := "ADD file:" + fileID + " in /"
	run := "/bin/sh -c addgroup -g 1000 node && adduser -u 1000 -G node -s /bin/sh -D node && apk add --no-cache libstdc++ && echo this-tail-must-remain"
	blob := fmt.Sprintf(`{
		"config": {"Cmd": ["nginx"]},
		"history": [
			{"created_by": "/bin/sh -c #(nop) %s"},
			{"created_by": "/bin/sh -c #(nop) ENV NODE_VERSION=10.15.2", "empty_layer": true},
			{"created_by": %q}
		]
	}`, add, run)

	h, q, store := manifestTestRouter(t)
	configDigest := manifestTestPutBlob(t, store, blob)
	payload := fmt.Sprintf(`{
		"schemaVersion": 2,
		"mediaType": "application/vnd.docker.distribution.manifest.v2+json",
		"config": {
			"mediaType": "application/vnd.docker.container.image.v1+json",
			"digest": %q,
			"size": %d
		},
		"layers": [
			{
				"mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
				"digest": "sha256:%s",
				"size": 1024
			},
			{
				"mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
				"digest": "sha256:%s",
				"size": 2048
			}
		]
	}`, configDigest, len(blob), strings.Repeat("b", 64), strings.Repeat("c", 64))
	digest := manifestTestSeed(t, q, "test", payload,
		"application/vnd.docker.distribution.manifest.v2+json")

	rec := manifestTestGET(t, h, "/dashboard/repos/test/manifests/"+digest)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-testid="image-layers"`) {
		t.Fatal("body missing image layers section")
	}
	if got := strings.Count(body, `data-testid="image-layer"`); got != 3 {
		t.Fatalf("image-layer count = %d, want 3", got)
	}
	// & is escaped in HTML; the tail and the middle of the RUN still have to
	// be present, which is what a truncated command would drop.
	for _, want := range []string{
		add,
		"ENV NODE_VERSION=10.15.2",
		"addgroup -g 1000 node",
		"adduser -u 1000 -G node -s /bin/sh -D node",
		"apk add --no-cache libstdc++",
		"this-tail-must-remain",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing full command text %q", want)
		}
	}
	if strings.Contains(body, "#(nop)") {
		t.Error("body should drop the #(nop) wrapper, not the instruction")
	}
	if strings.Contains(body, "file …") || strings.Contains(body, "file ...") {
		t.Error("ADD file digest was shortened")
	}
	addAt := strings.Index(body, add)
	envAt := strings.Index(body, "ENV NODE_VERSION=10.15.2")
	runAt := strings.Index(body, "this-tail-must-remain")
	if addAt < 0 || envAt < addAt || runAt < envAt {
		t.Errorf("history order wrong: add=%d env=%d run=%d", addAt, envAt, runAt)
	}

	d := dashboard.BuildManifestDisplay(t.Context(), db.Manifest{
		Digest:    digest,
		MediaType: "application/vnd.docker.distribution.manifest.v2+json",
		Payload:   []byte(payload),
		Size:      int64(len(payload)),
	}, store)
	if len(d.ImageLayers) != 3 {
		t.Fatalf("ImageLayers len = %d, want 3", len(d.ImageLayers))
	}
	if d.ImageLayers[0].Command != add || d.ImageLayers[0].Size != 1024 || d.ImageLayers[0].Digest == "" {
		t.Errorf("layer 0 = %+v, want full ADD at 1024 with a digest", d.ImageLayers[0])
	}
	if d.ImageLayers[1].Size != 0 || d.ImageLayers[1].Digest != "" {
		t.Errorf("empty ENV layer = %+v, want 0 B and no digest", d.ImageLayers[1])
	}
	if d.ImageLayers[2].Command != run || d.ImageLayers[2].Size != 2048 {
		t.Errorf("layer 2 = %+v, want the full RUN at 2048", d.ImageLayers[2])
	}
}
