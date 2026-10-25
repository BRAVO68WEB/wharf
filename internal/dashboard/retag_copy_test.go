package dashboard_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BRAVO68WEB/wharf/internal/db"
)

// Cross-repository and overwrite coverage for the retag flow: a retag to
// another repository copies the manifest row and its blob_refs edges (what a
// push of the same manifest would write), creating the target repository when
// it does not exist yet, and an existing target tag is overwritten.

// retagTestDigests returns the digests manifestTestImagePayload references:
// its config and its two layers.
func retagTestDigests() []string {
	return []string{
		"sha256:" + strings.Repeat("a", 64),
		"sha256:" + strings.Repeat("b", 64),
		"sha256:" + strings.Repeat("c", 64),
	}
}

func retagAPI(t *testing.T, h http.Handler, repo, tag, targetRepo, targetTag string) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Sprintf(`{"targetRepo":%q,"targetTag":%q}`, targetRepo, targetTag)
	return apiDo(t, h, http.MethodPost, "/api/repo/"+repo+"/tags/"+tag+"/retag", body)
}

// Given: repo "test" with one image manifest tagged "latest".
// When:  retagging to "v1.0" in the repo "other".
// Then:  "other" is created, the manifest row and its blob_refs edges are
// copied over, "other:v1.0" resolves, and "test:latest" is unchanged.
func Test_retag_when_targetRepoDiffers_then_manifestAndRefsCopied(t *testing.T) {
	h, q, _ := apiTestRouter(t)
	ctx := t.Context()
	digest := manifestTestSeed(t, q, "test", manifestTestImagePayload,
		"application/vnd.docker.distribution.manifest.v2+json")

	rec := retagAPI(t, h, "test", "latest", "other", "v1.0")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	other, err := q.GetRepositoryByName(ctx, "other")
	if err != nil {
		t.Fatalf("GetRepositoryByName(other): %v", err)
	}
	copied, err := q.GetManifest(ctx, db.GetManifestParams{RepoID: other.ID, Digest: digest})
	if err != nil {
		t.Fatalf("GetManifest(other, %s): %v", digest, err)
	}
	if string(copied.Payload) != manifestTestImagePayload {
		t.Errorf("copied payload differs from the source payload")
	}
	if copied.MediaType != "application/vnd.docker.distribution.manifest.v2+json" {
		t.Errorf("copied media type = %q", copied.MediaType)
	}
	if copied.Size != int64(len(manifestTestImagePayload)) {
		t.Errorf("copied size = %d, want %d", copied.Size, len(manifestTestImagePayload))
	}

	for _, blob := range retagTestDigests() {
		if _, err := q.GetBlobRef(ctx, db.GetBlobRefParams{Digest: blob, RepoID: other.ID}); err != nil {
			t.Errorf("blob_refs missing %s in other: %v", blob, err)
		}
	}

	srcRepo, err := q.GetRepositoryByName(ctx, "test")
	if err != nil {
		t.Fatalf("GetRepositoryByName(test): %v", err)
	}
	srcTag, err := q.GetTag(ctx, db.GetTagParams{RepoID: srcRepo.ID, Name: "latest"})
	if err != nil {
		t.Fatalf("GetTag(test:latest): %v", err)
	}
	if srcTag.ManifestDigest != digest {
		t.Errorf("test:latest digest = %q, want %q (original must not move)", srcTag.ManifestDigest, digest)
	}
	dstTag, err := q.GetTag(ctx, db.GetTagParams{RepoID: other.ID, Name: "v1.0"})
	if err != nil {
		t.Fatalf("GetTag(other:v1.0): %v", err)
	}
	if dstTag.ManifestDigest != digest {
		t.Errorf("other:v1.0 digest = %q, want %q", dstTag.ManifestDigest, digest)
	}
}

// Given: repo "test" whose "v1.0" already points at a second manifest.
// When:  "latest" is retagged onto the name "v1.0" in the same repo.
// Then:  the existing tag is overwritten (UPSERT) to the source digest and
// the source tag stays where it was.
func Test_retag_when_targetTagExists_then_upserts(t *testing.T) {
	h, q, _ := apiTestRouter(t)
	ctx := t.Context()
	srcDigest := manifestTestSeed(t, q, "test", manifestTestImagePayload,
		"application/vnd.docker.distribution.manifest.v2+json")

	repo, err := q.GetRepositoryByName(ctx, "test")
	if err != nil {
		t.Fatalf("GetRepositoryByName: %v", err)
	}
	otherDigest := "sha256:" + strings.Repeat("d", 64)
	if err := q.UpsertManifest(ctx, db.UpsertManifestParams{
		RepoID:    repo.ID,
		Digest:    otherDigest,
		MediaType: "application/vnd.docker.distribution.manifest.v2+json",
		Payload:   []byte(`{"schemaVersion":2}`),
		Size:      16,
	}); err != nil {
		t.Fatalf("UpsertManifest: %v", err)
	}
	if err := q.SetTag(ctx, db.SetTagParams{
		RepoID:         repo.ID,
		Name:           "v1.0",
		ManifestDigest: otherDigest,
		UpdatedAt:      1,
	}); err != nil {
		t.Fatalf("SetTag: %v", err)
	}

	rec := retagAPI(t, h, "test", "latest", "test", "v1.0")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	tag, err := q.GetTag(ctx, db.GetTagParams{RepoID: repo.ID, Name: "v1.0"})
	if err != nil {
		t.Fatalf("GetTag(v1.0): %v", err)
	}
	if tag.ManifestDigest != srcDigest {
		t.Errorf("v1.0 digest = %q, want %q (upsert must overwrite)", tag.ManifestDigest, srcDigest)
	}
	latest, err := q.GetTag(ctx, db.GetTagParams{RepoID: repo.ID, Name: "latest"})
	if err != nil {
		t.Fatalf("GetTag(latest): %v", err)
	}
	if latest.ManifestDigest != srcDigest {
		t.Errorf("latest digest = %q, want %q", latest.ManifestDigest, srcDigest)
	}
}

// Given: a target repository that does not exist yet.
// When:  a retag names it.
// Then:  the repository is created (push semantics) and the new tag resolves
// inside it.
func Test_retag_when_targetRepoMissing_then_created(t *testing.T) {
	h, q, _ := apiTestRouter(t)
	ctx := t.Context()
	digest := manifestTestSeed(t, q, "test", manifestTestImagePayload,
		"application/vnd.docker.distribution.manifest.v2+json")

	rec := retagAPI(t, h, "test", "latest", "team/app", "stable")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	other, err := q.GetRepositoryByName(ctx, "team/app")
	if err != nil {
		t.Fatalf("GetRepositoryByName(team/app): %v", err)
	}
	tag, err := q.GetTag(ctx, db.GetTagParams{RepoID: other.ID, Name: "stable"})
	if err != nil {
		t.Fatalf("GetTag(team/app:stable): %v", err)
	}
	if tag.ManifestDigest != digest {
		t.Errorf("stable digest = %q, want %q", tag.ManifestDigest, digest)
	}
}

// Given: the target repo already carries the same manifest digest.
// When:  a retag copies across.
// Then:  the existing manifest row is left alone (no clobber) and only the
// new tag is written.
func Test_retag_when_targetAlreadyHasManifest_then_rowLeftAlone(t *testing.T) {
	h, q, _ := apiTestRouter(t)
	ctx := t.Context()
	digest := manifestTestSeed(t, q, "test", manifestTestImagePayload,
		"application/vnd.docker.distribution.manifest.v2+json")

	other, err := q.CreateRepository(ctx, "other")
	if err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}
	if err := q.UpsertManifest(ctx, db.UpsertManifestParams{
		RepoID:    other.ID,
		Digest:    digest,
		MediaType: "application/vnd.docker.distribution.manifest.v2+json",
		Payload:   []byte(manifestTestImagePayload),
		Size:      999, // sentinel: a copy would rewrite it to the real size
	}); err != nil {
		t.Fatalf("UpsertManifest: %v", err)
	}

	rec := retagAPI(t, h, "test", "latest", "other", "v1.0")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	kept, err := q.GetManifest(ctx, db.GetManifestParams{RepoID: other.ID, Digest: digest})
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	if kept.Size != 999 {
		t.Errorf("manifest size = %d, want 999 (existing row must not be rewritten)", kept.Size)
	}
	if _, err := q.GetTag(ctx, db.GetTagParams{RepoID: other.ID, Name: "v1.0"}); err != nil {
		t.Errorf("GetTag(other:v1.0): %v", err)
	}
}

// Given: repo "test" whose "latest" points at an image index whose entries
// are manifest rows of the same repo (multi-arch image).
// When:  the index tag is retagged into "other".
// Then:  the child manifest rows are copied too, so the destination can
// serve the whole image, not just the index.
func Test_retag_when_indexManifest_then_childrenCopied(t *testing.T) {
	h, q, _ := apiTestRouter(t)
	ctx := t.Context()
	indexDigest := manifestTestSeed(t, q, "test", manifestTestIndexPayload,
		"application/vnd.oci.image.index.v1+json")

	src, err := q.GetRepositoryByName(ctx, "test")
	if err != nil {
		t.Fatalf("GetRepositoryByName: %v", err)
	}
	children := map[string]string{
		"sha256:" + strings.Repeat("d", 64): `{"schemaVersion":2,"child":1}`,
		"sha256:" + strings.Repeat("e", 64): `{"schemaVersion":2,"child":2}`,
	}
	for digest, payload := range children {
		if err := q.UpsertManifest(ctx, db.UpsertManifestParams{
			RepoID:    src.ID,
			Digest:    digest,
			MediaType: "application/vnd.oci.image.manifest.v1+json",
			Payload:   []byte(payload),
			Size:      int64(len(payload)),
		}); err != nil {
			t.Fatalf("UpsertManifest(%s): %v", digest, err)
		}
	}

	rec := retagAPI(t, h, "test", "latest", "other", "v1.0")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	other, err := q.GetRepositoryByName(ctx, "other")
	if err != nil {
		t.Fatalf("GetRepositoryByName(other): %v", err)
	}
	if _, err := q.GetManifest(ctx, db.GetManifestParams{RepoID: other.ID, Digest: indexDigest}); err != nil {
		t.Fatalf("index manifest not copied: %v", err)
	}
	for digest, payload := range children {
		m, err := q.GetManifest(ctx, db.GetManifestParams{RepoID: other.ID, Digest: digest})
		if err != nil {
			t.Fatalf("child %s not copied: %v", digest, err)
		}
		if string(m.Payload) != payload {
			t.Errorf("child %s payload = %q, want %q", digest, m.Payload, payload)
		}
	}
}
