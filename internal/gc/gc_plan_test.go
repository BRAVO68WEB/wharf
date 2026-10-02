package gc_test

import (
	"slices"
	"testing"

	"github.com/user/better-docker-registry/internal/db"
	"github.com/user/better-docker-registry/internal/gc"
)

// Planning tests (dry-run listing, stats summary, corrupt-payload fallback).
// Fixtures live in gc_fixtures_test.go; the real sweep is gc_sweep_test.go.

func Test_DryRun_when_unreferencedBlobs_then_listsExactlyThem(t *testing.T) {
	// Given: 5 blobs in storage; a manifest referencing 2 of them.
	ctx := t.Context()
	q := gcTestDB(t)
	store := newGCTestStore()
	repo, err := q.CreateRepository(ctx, "app/base")
	if err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	config := gcTestPut(t, store, "config-bytes")
	layer := gcTestPut(t, store, "layer-bytes")
	orphan1 := gcTestPut(t, store, "orphan-one")
	orphan2 := gcTestPut(t, store, "orphan-two")
	orphan3 := gcTestPut(t, store, "orphan-three")

	manifestDigest := gcTestDigest(1)
	gcTestManifest(t, q, repo.ID, manifestDigest, gcTestManifestPayload(config, layer))

	runner := gc.NewRunner(q, store)

	// When: a dry run executes.
	got, err := runner.DryRun(ctx)
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}

	// Then: exactly the 3 unreferenced blobs are listed with their sizes.
	want := []gc.Blob{
		{Digest: orphan1, Size: int64(len("orphan-one"))},
		{Digest: orphan2, Size: int64(len("orphan-two"))},
		{Digest: orphan3, Size: int64(len("orphan-three"))},
	}
	slices.SortFunc(want, func(a, b gc.Blob) int {
		if a.Digest < b.Digest {
			return -1
		}
		return 1
	})
	if len(got) != len(want) {
		t.Fatalf("DryRun listed %d blobs %v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("blob[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}

	// And: the store is untouched.
	if len(store.blobs) != 5 {
		t.Fatalf("store holds %d blobs after dry run, want 5", len(store.blobs))
	}
}

func Test_Stats_when_mixedInventory_then_countsAndBytes(t *testing.T) {
	// Given: 3 blobs, 1 referenced.
	ctx := t.Context()
	q := gcTestDB(t)
	store := newGCTestStore()
	repo, err := q.CreateRepository(ctx, "app/base")
	if err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	live := gcTestPut(t, store, "live")
	gcTestPut(t, store, "dead-1")
	gcTestPut(t, store, "dead-two!")
	gcTestManifest(t, q, repo.ID, gcTestDigest(1), gcTestManifestPayload(live))

	// When: stats are requested.
	s, err := gc.NewRunner(q, store).Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}

	// Then: totals split into referenced and unreferenced with byte sum.
	wantBytes := int64(len("dead-1") + len("dead-two!"))
	if s.TotalBlobs != 3 || s.ReferencedBlobs != 1 || s.UnreferencedBlobs != 2 || s.UnreferencedBytes != wantBytes {
		t.Fatalf("stats = %+v, want total=3 referenced=1 unreferenced=2 bytes=%d", s, wantBytes)
	}
}

func Test_DryRun_when_manifestPayloadUnparseable_then_retainsRepoBlobRefs(t *testing.T) {
	// Given: a manifest with corrupt JSON whose repo recorded blob_refs edges
	// for two blobs, plus one true orphan in storage.
	ctx := t.Context()
	q := gcTestDB(t)
	store := newGCTestStore()
	repo, err := q.CreateRepository(ctx, "app/base")
	if err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	kept1 := gcTestPut(t, store, "kept-one")
	kept2 := gcTestPut(t, store, "kept-two")
	orphan := gcTestPut(t, store, "true-orphan")

	for i, d := range []string{kept1, kept2} {
		if err := q.UpsertBlobMetadata(ctx, db.UpsertBlobMetadataParams{
			Digest: d, Size: 1, MediaType: "application/octet-stream",
		}); err != nil {
			t.Fatalf("UpsertBlobMetadata %d: %v", i, err)
		}
		if err := q.AddBlobRef(ctx, db.AddBlobRefParams{Digest: d, RepoID: repo.ID}); err != nil {
			t.Fatalf("AddBlobRef %d: %v", i, err)
		}
	}
	gcTestManifest(t, q, repo.ID, gcTestDigest(1), "{not json")

	// When: a dry run executes.
	got, err := gc.NewRunner(q, store).DryRun(ctx)
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}

	// Then: only the true orphan is collectable; the broken manifest's
	// recorded refs are over-retained, never swept.
	if len(got) != 1 || got[0].Digest != orphan {
		t.Fatalf("DryRun = %v, want only %s", got, orphan)
	}
}
