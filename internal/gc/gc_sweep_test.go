package gc_test

import (
	"slices"
	"testing"

	"github.com/user/better-docker-registry/internal/db"
	"github.com/user/better-docker-registry/internal/gc"
)

// Sweep tests: the real mark-and-sweep run. Fixtures live in
// gc_fixtures_test.go; the dry-run/plan side is gc_plan_test.go.

func Test_Run_when_sweep_then_deletesExactlyDryRunSet_when_referencedSurvive(t *testing.T) {
	// Given: 5 blobs with 2 referenced by a stored manifest.
	ctx := t.Context()
	q := gcTestDB(t)
	store := newGCTestStore()
	repo, err := q.CreateRepository(ctx, "app/base")
	if err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	config := gcTestPut(t, store, "config-bytes")
	layer := gcTestPut(t, store, "layer-bytes")
	gcTestPut(t, store, "orphan-one")
	gcTestPut(t, store, "orphan-two")
	gcTestPut(t, store, "orphan-three")

	manifestDigest := gcTestDigest(1)
	gcTestManifest(t, q, repo.ID, manifestDigest, gcTestManifestPayload(config, layer))
	if err := q.UpsertBlobMetadata(ctx, db.UpsertBlobMetadataParams{
		Digest: config, Size: int64(len("config-bytes")), MediaType: "application/vnd.oci.image.config.v1+json",
	}); err != nil {
		t.Fatalf("UpsertBlobMetadata: %v", err)
	}
	if err := q.AddBlobRef(ctx, db.AddBlobRefParams{Digest: config, RepoID: repo.ID}); err != nil {
		t.Fatalf("AddBlobRef: %v", err)
	}

	runner := gc.NewRunner(q, store)
	dry, err := runner.DryRun(ctx)
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}

	// When: a real sweep runs.
	res, err := runner.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Then: exactly the dry-run set was deleted, nothing more.
	if res.Failed != 0 {
		t.Fatalf("Failed = %d, want 0", res.Failed)
	}
	if len(res.Deleted) != 3 {
		t.Fatalf("Deleted = %v, want 3 blobs", res.Deleted)
	}
	dryDigests := []string{dry[0].Digest, dry[1].Digest, dry[2].Digest}
	for _, b := range res.Deleted {
		if !slices.Contains(dryDigests, b.Digest) {
			t.Fatalf("deleted %s, which dry run did not list", b.Digest)
		}
		if _, ok := store.blobs[b.Digest]; ok {
			t.Fatalf("blob %s still in store after sweep", b.Digest)
		}
	}

	// And: the referenced blobs survive and are still readable.
	for _, live := range []string{config, layer} {
		rc, err := store.Get(ctx, live)
		if err != nil {
			t.Fatalf("Get surviving %s: %v", live, err)
		}
		_ = rc.Close()
	}

	// And: the swept blobs' metadata and ref edges are gone.
	if _, err := q.GetBlobMetadata(ctx, dry[0].Digest); err == nil {
		t.Fatalf("blob_metadata for %s survived the sweep", dry[0].Digest)
	}
	refs, err := q.ListBlobRefsByRepo(ctx, repo.ID)
	if err != nil {
		t.Fatalf("ListBlobRefsByRepo: %v", err)
	}
	for _, ref := range refs {
		if ref.Digest == dry[0].Digest {
			t.Fatalf("blob_refs edge for %s survived the sweep", dry[0].Digest)
		}
	}

	// And: a second dry run finds nothing left to collect.
	again, err := runner.DryRun(ctx)
	if err != nil {
		t.Fatalf("DryRun after sweep: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second dry run listed %v, want empty", again)
	}
}
