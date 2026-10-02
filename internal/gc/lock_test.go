package gc_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/user/better-docker-registry/internal/gc"
	"github.com/user/better-docker-registry/internal/storage"
)

// Helpers are gcTest-prefixed: package gc_test is shared with the engine
// tests (gc_test.go). Naming follows the task-4 test convention
// (Test_<Behavior>_when_<Condition> with Given/When/Then blocks).

// gcTestEcho is a handler that records it ran and answers 204.
func gcTestEcho() (http.Handler, *int) {
	var hits int
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusNoContent)
	})
	return h, &hits
}

func Test_WriteGuard_when_idle_then_writesAndReadsPass(t *testing.T) {
	// Given: a quiesce lock with no collection in flight.
	l := gc.NewLock()
	h, hits := gcTestEcho()
	guarded := l.WriteGuard(h)

	// When: a write and a read arrive.
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodGet} {
		rec := httptest.NewRecorder()
		guarded.ServeHTTP(rec, httptest.NewRequest(method, "/v2/x", nil))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("%s: status = %d, want 204", method, rec.Code)
		}
	}

	// Then: both were admitted to the wrapped handler.
	if *hits != 3 {
		t.Fatalf("hits = %d, want 3", *hits)
	}
}

func Test_WriteGuard_when_collectionRunning_then_writeRefused503_when_readStillPasses(t *testing.T) {
	// Given: a collection holding the quiesce lock.
	l := gc.NewLock()
	release, ok := l.BeginGC()
	if !ok {
		t.Fatal("BeginGC: ok = false, want true")
	}
	defer release()

	h, hits := gcTestEcho()
	guarded := l.WriteGuard(h)

	// When: a /v2 write request arrives mid-collection.
	rec := httptest.NewRecorder()
	guarded.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v2/repo/blobs/uploads/", nil))

	// Then: it is refused with 503 and the OCI UNAVAILABLE envelope.
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got == "" {
		t.Fatal("Retry-After missing")
	}
	var body storage.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Errors) != 1 || body.Errors[0].Code != storage.ErrorCodeUnavailable {
		t.Fatalf("errors = %+v, want one UNAVAILABLE", body.Errors)
	}
	if *hits != 0 {
		t.Fatalf("hits = %d, want 0 (handler must not run)", *hits)
	}

	// And: a pull (GET) is still served during the collection.
	rec = httptest.NewRecorder()
	guarded.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v2/repo/blobs/sha256:abc", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("GET: status = %d, want 204", rec.Code)
	}
}

func Test_BeginGC_when_secondRunThenRefused_when_firstStillHolds(t *testing.T) {
	// Given: a collection already in flight.
	l := gc.NewLock()
	release, ok := l.BeginGC()
	if !ok {
		t.Fatal("BeginGC: ok = false, want true")
	}
	defer release()

	// When: a second run is requested (dashboard double-submit).
	_, ok2 := l.BeginGC()

	// Then: it is refused rather than queued behind the first sweep.
	if ok2 {
		t.Fatal("second BeginGC: ok = true, want false")
	}
	if !l.Running() {
		t.Fatal("Running() = false while first run holds the lock")
	}
}

func Test_BeginGC_when_writeInFlight_then_quiesces_when_releaseReopens(t *testing.T) {
	// Given: one write admitted before the collection starts.
	l := gc.NewLock()
	writeRelease, ok := l.BeginWrite()
	if !ok {
		t.Fatal("BeginWrite: ok = false, want true")
	}

	// When: a collection starts (it must drain the in-flight write first)
	// and the write later finishes.
	done := make(chan struct{})
	go func() {
		gcRelease, gcOK := l.BeginGC()
		if !gcOK {
			t.Error("BeginGC: ok = false, want true")
			return
		}
		<-done
		gcRelease()
	}()

	// Then: once the collection registers, new writes are refused even while
	// it is still draining the old one (quiesce front-loads the refusal).
	deadline := time.Now().Add(2 * time.Second)
	for !l.Running() {
		if time.Now().After(deadline) {
			t.Fatal("collection never registered")
		}
	}
	if _, writeOK := l.BeginWrite(); writeOK {
		t.Fatal("BeginWrite during drain: ok = true, want false")
	}

	// And: letting the in-flight write finish unblocks the sweep, and once
	// the run is released the lock admits writes again.
	writeRelease()
	close(done)
	deadline = time.Now().Add(2 * time.Second)
	for {
		release, writeOK := l.BeginWrite()
		if writeOK {
			release()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("BeginWrite never reopened after the collection ended")
		}
	}
}

func Test_WriteGuard_when_quiescedBody_then_matchesOCIEnvelopeShape(t *testing.T) {
	// Given: a guarded handler during a collection.
	l := gc.NewLock()
	release, ok := l.BeginGC()
	if !ok {
		t.Fatal("BeginGC: ok = false, want true")
	}
	defer release()
	guarded := l.WriteGuard(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	// When: a DELETE arrives.
	rec := httptest.NewRecorder()
	guarded.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/v2/r/manifests/sha256:abc", nil))

	// Then: the body is byte-shaped like every other /v2 error.
	if !strings.Contains(rec.Body.String(), `"code":"UNAVAILABLE"`) {
		t.Fatalf("body = %s, want UNAVAILABLE code", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
}
