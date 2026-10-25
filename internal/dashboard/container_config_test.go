package dashboard_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/BRAVO68WEB/wharf/internal/dashboard"
	"github.com/BRAVO68WEB/wharf/internal/db"
)

// Container config tests: manifest detail reads the image config blob from
// blob storage and turns its runtime fields into structured data.

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

// A missing config blob omits container config and keeps the layer list.
func Test_buildManifestDisplay_when_configBlobMissing_then_noContainerConfig(t *testing.T) {
	m := db.Manifest{
		Digest:    "sha256:" + strings.Repeat("c", 64),
		MediaType: "application/vnd.docker.distribution.manifest.v2+json",
		Payload:   []byte(manifestTestImagePayload),
		Size:      int64(len(manifestTestImagePayload)),
	}
	d := dashboard.BuildManifestDisplay(t.Context(), m, newGCTestStore())
	if d.ContainerConfig != nil {
		t.Fatalf("ContainerConfig = %#v, want nil", d.ContainerConfig)
	}
	if len(d.Layers) != 2 {
		t.Fatalf("Layers len = %d, want 2", len(d.Layers))
	}
}

// A config blob that is not JSON omits container config.
func Test_buildManifestDisplay_when_configBlobMalformed_then_noContainerConfig(t *testing.T) {
	store := newGCTestStore()
	configDigest := manifestTestPutBlob(t, store, "{not json")
	m := db.Manifest{
		Digest:    "sha256:" + strings.Repeat("c", 64),
		MediaType: "application/vnd.docker.distribution.manifest.v2+json",
		Payload:   []byte(manifestTestPayloadWithConfig(configDigest, 9)),
	}
	d := dashboard.BuildManifestDisplay(t.Context(), m, store)
	if d.ContainerConfig != nil {
		t.Fatalf("ContainerConfig = %#v, want nil", d.ContainerConfig)
	}
	if len(d.ImageLayers) != 0 {
		t.Fatalf("ImageLayers len = %d, want 0", len(d.ImageLayers))
	}
}

// History keeps every command whole, marks empty steps as 0 bytes, and pairs
// real steps with their layer size, oldest first.
func Test_buildManifestDisplay_when_history_then_fullLayerCommands(t *testing.T) {
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

	store := newGCTestStore()
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

	d := dashboard.BuildManifestDisplay(t.Context(), db.Manifest{
		Digest:    "sha256:" + strings.Repeat("d", 64),
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
	if d.ImageLayers[1].Command != "ENV NODE_VERSION=10.15.2" {
		t.Errorf("empty ENV command = %q", d.ImageLayers[1].Command)
	}
	if d.ImageLayers[1].Size != 0 || d.ImageLayers[1].Digest != "" {
		t.Errorf("empty ENV layer = %+v, want 0 B and no digest", d.ImageLayers[1])
	}
	if d.ImageLayers[2].Command != run || d.ImageLayers[2].Size != 2048 {
		t.Errorf("layer 2 = %+v, want the full RUN at 2048", d.ImageLayers[2])
	}
	if strings.Contains(d.ImageLayers[0].Command, "#(nop)") || strings.Contains(d.ImageLayers[2].Command, "…") {
		t.Errorf("commands were wrapped or shortened: %+v", d.ImageLayers)
	}
}
