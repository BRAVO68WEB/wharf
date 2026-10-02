package dashboard_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/user/better-docker-registry/internal/gc"
)

func Test_serveRun_when_posted_then_deletesExactlyDryRunSet(t *testing.T) {
	// Given: the standard 5-blob fixture and a dry run already observed.
	lock := gc.NewLock()
	h, q, store := gcTestRouter(t, lock)
	seed := gcTestSeedFive(t, q, store)
	dry := gcTestRequest(t, h, http.MethodPost, "/dashboard/gc/dry-run").Body.String()

	// When: the real GC button posts (after the operator confirmed).
	rec := gcTestRequest(t, h, http.MethodPost, "/dashboard/gc/run")

	// Then: the results report 3 deleted blobs and the bytes freed.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Collection complete: 3 blob(s) deleted") {
		t.Fatalf("missing run heading in %q", body)
	}
	if !strings.Contains(body, `data-testid="gc-freed"`) || !strings.Contains(body, "Bytes freed: ") {
		t.Fatalf("missing gc-freed summary in %q", body)
	}

	// And: exactly the dry-run digests are gone; referenced blobs survive.
	for _, orphan := range seed.Orphans {
		if !strings.Contains(dry, orphan) {
			t.Fatalf("dry run did not list %s", orphan)
		}
		if store.hasBlob(orphan) {
			t.Fatalf("orphan %s survived the sweep", orphan)
		}
		if !strings.Contains(body, orphan) {
			t.Fatalf("run results missing deleted blob %s", orphan)
		}
	}
	for _, live := range []string{seed.Config, seed.Layer} {
		if !store.hasBlob(live) {
			t.Fatalf("referenced blob %s was deleted", live)
		}
	}

	// And: a follow-up dry run finds nothing left to collect.
	again := gcTestRequest(t, h, http.MethodPost, "/dashboard/gc/dry-run").Body.String()
	if !strings.Contains(again, "Nothing to collect.") {
		t.Fatalf("post-run dry run should be empty, got %q", again)
	}
}

func Test_serveRun_when_directPost_then_fullPageWithResults(t *testing.T) {
	// Given: the standard fixture and a non-HTMX (direct navigation) caller.
	lock := gc.NewLock()
	h, q, store := gcTestRouter(t, lock)
	gcTestSeedFive(t, q, store)

	// When: the run endpoint is posted without HTMX headers.
	req := httptest.NewRequest(http.MethodPost, "/dashboard/gc/run", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// Then: the full page renders with the results embedded.
	// templ normalises the doctype to lowercase in rendered output.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<!doctype html>") {
		t.Fatal("direct POST must render the full page")
	}
	if !strings.Contains(body, "Collection complete: 3 blob(s) deleted") {
		t.Fatalf("page missing run results in %q", body)
	}
	if n := store.blobCount(); n != 2 {
		t.Fatalf("store holds %d blobs after run, want 2", n)
	}
}

func Test_serveRun_when_gcAlreadyRunning_then_refused(t *testing.T) {
	// Given: a collection already holding the quiesce lock.
	lock := gc.NewLock()
	release, ok := lock.BeginGC()
	if !ok {
		t.Fatal("BeginGC: ok = false, want true")
	}
	defer release()
	h, _, store := gcTestRouter(t, lock)
	before := store.blobCount()

	// When: a second run posts (dashboard double-submit).
	rec := gcTestRequest(t, h, http.MethodPost, "/dashboard/gc/run")

	// Then: it is refused with an error fragment and deletes nothing.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (error fragment)", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, `data-testid="gc-error"`) || !strings.Contains(body, "already running") {
		t.Fatalf("missing already-running error in %q", body)
	}
	if n := store.blobCount(); n != before {
		t.Fatalf("store changed during refused run: %d != %d", n, before)
	}
}
