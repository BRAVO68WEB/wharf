package dashboard_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/BRAVO68WEB/wharf/internal/db"
)

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
	digest := "sha256:" + sha256Hex(payload)
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

// sha256Hex returns the hex-encoded SHA-256 of s (without the sha256: prefix).
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
