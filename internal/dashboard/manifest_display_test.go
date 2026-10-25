package dashboard_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/user/better-docker-registry/internal/dashboard"
	"github.com/user/better-docker-registry/internal/db"
)

// Display-model tests: the pure parsing behind the manifest detail view —
// manifest payload to structured fields, and digest shortening for the layer
// table. No HTTP, no storage.

// buildManifestDisplay: image manifest produces config digest + layer list.
// The blob store is nil here: this covers manifest parsing only.
func Test_buildManifestDisplay_when_imageManifest_then_configAndLayers(t *testing.T) {
	m := db.Manifest{
		Digest:    "sha256:" + strings.Repeat("a", 64),
		MediaType: "application/vnd.docker.distribution.manifest.v2+json",
		Payload:   []byte(manifestTestImagePayload),
		Size:      int64(len(manifestTestImagePayload)),
	}
	d := dashboard.BuildManifestDisplay(t.Context(), m, nil)
	if d.ConfigDigest != "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Errorf("ConfigDigest = %q", d.ConfigDigest)
	}
	if d.ContainerConfig != nil {
		t.Errorf("ContainerConfig = %#v, want nil with no blob store", d.ContainerConfig)
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
	d := dashboard.BuildManifestDisplay(t.Context(), m, nil)
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

// TruncateDigest keeps the algorithm and 12 hex chars from each end.
func Test_TruncateDigest_when_longDigest_then_shortened(t *testing.T) {
	full := "sha256:" + strings.Repeat("ab", 32)
	want := "sha256:abababababab…abababababab"
	if got := dashboard.TruncateDigest(full); got != want {
		t.Errorf("TruncateDigest = %q, want %q", got, want)
	}
	if got := dashboard.TruncateDigest("sha256:abcd"); got != "sha256:abcd" {
		t.Errorf("TruncateDigest(short) = %q, want unchanged", got)
	}
	if got := dashboard.TruncateDigest("nocolon"); got != "nocolon" {
		t.Errorf("TruncateDigest(nocolon) = %q, want unchanged", got)
	}
}
