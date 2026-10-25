package gc_test

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/BRAVO68WEB/wharf/internal/gc"
	"github.com/BRAVO68WEB/wharf/internal/storage"
)

// Task-19 hardening acceptance: active-upload protection, batch deletes, and
// the 404/503 boundary contracts. Fixtures live in gc_fixtures_test.go; the
// planning and sweep basics are gc_plan_test.go and gc_sweep_test.go, the
// progress callback is gc_progress_test.go.

func Test_DryRun_when_activeUploadSession_then_blobNotListed(t *testing.T) {
	// Given: two unreferenced blobs where an active upload session claims
	// one of them.
	ctx := t.Context()
	q := gcTestDB(t)
	store := newGCTestStore()
	repo, err := q.CreateRepository(ctx, "app/base")
	if err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}
	claimed := gcTestPut(t, store, "claimed-by-upload")
	orphan := gcTestPut(t, store, "true-orphan")
	gcTestUpload(t, q, repo.ID, claimed, time.Now().Unix()+3600)

	// When: a dry run executes.
	got, err := gc.NewRunner(q, store).DryRun(ctx)
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}

	// Then: only the unclaimed orphan would be collected.
	if len(got) != 1 || got[0].Digest != orphan {
		t.Fatalf("DryRun = %v, want only %s", got, orphan)
	}
}

func Test_Run_when_activeUploadSession_then_blobSurvivesSweep(t *testing.T) {
	// Given: an unreferenced blob claimed by an active upload session and a
	// true orphan.
	ctx := t.Context()
	q := gcTestDB(t)
	store := newGCTestStore()
	repo, err := q.CreateRepository(ctx, "app/base")
	if err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}
	claimed := gcTestPut(t, store, "claimed-by-upload")
	orphan := gcTestPut(t, store, "true-orphan")
	gcTestUpload(t, q, repo.ID, claimed, time.Now().Unix()+3600)

	// When: a real sweep runs.
	res, err := gc.NewRunner(q, store).Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Then: only the orphan is deleted; the claimed blob is still GETtable.
	if len(res.Deleted) != 1 || res.Deleted[0].Digest != orphan {
		t.Fatalf("Deleted = %v, want only %s", res.Deleted, orphan)
	}
	rc, err := store.Get(ctx, claimed)
	if err != nil {
		t.Fatalf("Get claimed %s: %v", claimed, err)
	}
	_ = rc.Close()
}

func Test_Run_when_uploadSessionExpired_then_blobCollected(t *testing.T) {
	// Given: an unreferenced blob whose upload session expired an hour ago.
	ctx := t.Context()
	q := gcTestDB(t)
	store := newGCTestStore()
	repo, err := q.CreateRepository(ctx, "app/base")
	if err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}
	stale := gcTestPut(t, store, "expired-session-blob")
	gcTestUpload(t, q, repo.ID, stale, time.Now().Unix()-3600)

	// When: a real sweep runs.
	res, err := gc.NewRunner(q, store).Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Then: the expired session no longer claims the blob, so it is
	// collected like any other orphan.
	if len(res.Deleted) != 1 || res.Deleted[0].Digest != stale {
		t.Fatalf("Deleted = %v, want only %s", res.Deleted, stale)
	}
}

func Test_Run_when_batched_then_deletesExactlyCandidateSet(t *testing.T) {
	// Given: 5 unreferenced blobs and 1 manifest-referenced blob.
	ctx := t.Context()
	q := gcTestDB(t)
	store := newGCTestStore()
	repo, err := q.CreateRepository(ctx, "app/base")
	if err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}
	kept := gcTestPut(t, store, "kept-by-manifest")
	var want []gc.Blob
	for i := range 5 {
		content := fmt.Sprintf("orphan-%d", i)
		want = append(want, gc.Blob{Digest: gcTestPut(t, store, content), Size: int64(len(content))})
	}
	gcTestManifest(t, q, repo.ID, gcTestDigest(1), gcTestManifestPayload(kept))
	slices.SortFunc(want, func(a, b gc.Blob) int {
		return cmp.Compare(a.Digest, b.Digest)
	})

	// When: a real sweep runs with a delete-batch bound of 2.
	res, err := gc.NewRunner(q, store, gc.WithBatchSize(2)).Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Then: batching changes nothing — exactly the 5 candidates go, in
	// digest order, and the referenced blob stays GETtable.
	if res.Failed != 0 {
		t.Fatalf("Failed = %d, want 0", res.Failed)
	}
	if !slices.Equal(res.Deleted, want) {
		t.Fatalf("Deleted = %v, want %v", res.Deleted, want)
	}
	rc, err := store.Get(ctx, kept)
	if err != nil {
		t.Fatalf("Get surviving %s: %v", kept, err)
	}
	_ = rc.Close()
}

func Test_Run_when_blobDeleted_then_GetErrsBlobNotFound(t *testing.T) {
	// Given: 1 unreferenced blob and 1 manifest-referenced blob.
	ctx := t.Context()
	q := gcTestDB(t)
	store := newGCTestStore()
	repo, err := q.CreateRepository(ctx, "app/base")
	if err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}
	kept := gcTestPut(t, store, "kept-by-manifest")
	gone := gcTestPut(t, store, "swept-away")
	gcTestManifest(t, q, repo.ID, gcTestDigest(1), gcTestManifestPayload(kept))

	// When: a real sweep runs.
	res, err := gc.NewRunner(q, store).Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Deleted) != 1 || res.Deleted[0].Digest != gone {
		t.Fatalf("Deleted = %v, want only %s", res.Deleted, gone)
	}

	// Then: the swept blob answers ErrBlobNotFound, which the /v2 blob
	// route maps to 404 BLOB_UNKNOWN.
	if _, err := store.Get(ctx, gone); !errors.Is(err, storage.ErrBlobNotFound) {
		t.Fatalf("Get swept %s: err = %v, want ErrBlobNotFound", gone, err)
	}

	// And: the referenced blob is still GETtable.
	rc, err := store.Get(ctx, kept)
	if err != nil {
		t.Fatalf("Get surviving %s: %v", kept, err)
	}
	_ = rc.Close()
}

func Test_Run_when_pushDuringGC_then_writeRefused503(t *testing.T) {
	// Given: a quiesce lock held around a real sweep of two orphans.
	ctx := t.Context()
	q := gcTestDB(t)
	store := newGCTestStore()
	gcTestPut(t, store, "orphan-one")
	gcTestPut(t, store, "orphan-two")

	lock := gc.NewLock()
	release, ok := lock.BeginGC()
	if !ok {
		t.Fatal("BeginGC: ok = false, want true")
	}
	runDone := make(chan error, 1)
	go func() {
		_, err := gc.NewRunner(q, store).Run(ctx)
		runDone <- err
	}()

	// When: a push request arrives while the sweep is running.
	h, hits := gcTestEcho()
	guarded := lock.WriteGuard(h)
	rec := httptest.NewRecorder()
	guarded.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v2/app/base/blobs/uploads/", nil))

	// Then: it is refused with 503 and the OCI UNAVAILABLE envelope.
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if *hits != 0 {
		t.Fatalf("hits = %d, want 0 (handler must not run)", *hits)
	}
	var body storage.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Errors) != 1 || body.Errors[0].Code != storage.ErrorCodeUnavailable {
		t.Fatalf("errors = %+v, want one UNAVAILABLE", body.Errors)
	}

	// And: once the sweep completes and releases, the same push is admitted.
	if err := <-runDone; err != nil {
		t.Fatalf("Run: %v", err)
	}
	release()
	rec = httptest.NewRecorder()
	guarded.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v2/app/base/blobs/uploads/", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status after release = %d, want 204", rec.Code)
	}
}
