package dashboard_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/user/better-docker-registry/internal/api"
	"github.com/user/better-docker-registry/internal/gc"
	"github.com/user/better-docker-registry/internal/middleware"
)

// gcTestCSRFToken is the session-bound CSRF token the tests bind into the
// request context; middleware.CSRF compares the X-CSRF-Token header against
// exactly this value (task 14 contract).
const gcTestCSRFToken = "gc-test-csrf-token"

// gcTestWithCSRF wraps h so requests carry a bound CSRF token the way
// dashboard.WithSessionCSRF does for a signed-in operator. The binding runs
// BEFORE middleware.CSRF — CSRF only reads the token already in the context,
// which is exactly the WithSessionCSRF-after-RequireSession mount order task
// 15 pinned.
func gcTestWithCSRF(h http.Handler) http.Handler {
	bind := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(middleware.WithCSRFToken(r.Context(), gcTestCSRFToken)))
		})
	}
	return bind(middleware.CSRF(h))
}

func Test_gcPosts_when_noCSRFToken_then_403_when_tokenThenServed(t *testing.T) {
	// Given: the GC routes behind middleware.CSRF with a bound token.
	lock := gc.NewLock()
	inner, q, store := gcTestRouter(t, lock)
	h := gcTestWithCSRF(inner)
	seed := gcTestSeedFive(t, q, store)

	// When: both POSTs arrive without the X-CSRF-Token header.
	for _, path := range []string{"/api/gc/dry-run", "/api/gc/run"} {
		rec := gcTestRequest(t, h, http.MethodPost, path)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("POST %s without token: status = %d, want 403", path, rec.Code)
		}
	}

	// Then: nothing was collected.
	if n := store.blobCount(); n != 5 {
		t.Fatalf("store holds %d blobs after CSRF refusals, want 5", n)
	}

	// And: with the matching token both POSTs are served.
	for _, path := range []string{"/api/gc/dry-run", "/api/gc/run"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Header.Set(middleware.CSRFHeader, gcTestCSRFToken)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST %s with token: status = %d, want 200", path, rec.Code)
		}
	}
	if n := store.blobCount(); n != 2 {
		t.Fatalf("store holds %d blobs after authorized run, want 2", n)
	}
	for _, orphan := range seed.Orphans {
		if store.hasBlob(orphan) {
			t.Fatalf("orphan %s survived the authorized run", orphan)
		}
	}
}

func Test_v2Writes_when_gcRunning_then_503_when_readsStillServed(t *testing.T) {
	// Given: the /v2 tree wired with the same quiesce lock the GC handler
	// uses, and a collection holding that lock.
	lock := gc.NewLock()
	q := gcTestDB(t)
	store := newGCTestStore()
	v2 := api.NewRouter(store, q, api.WithWriteQuiesce(lock.WriteGuard))

	release, ok := lock.BeginGC()
	if !ok {
		t.Fatal("BeginGC: ok = false, want true")
	}
	defer release()

	// When: a push (PUT manifest) and a blob DELETE arrive mid-collection.
	deleteReq := httptest.NewRequest(http.MethodDelete,
		"/v2/app/base/blobs/sha256:0000000000000000000000000000000000000000000000000000000000000000", nil)
	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodPut, "/v2/app/base/manifests/latest", nil),
		deleteReq,
	} {
		rec := httptest.NewRecorder()
		v2.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s: status = %d, want 503", req.Method, req.URL.Path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `"code":"UNAVAILABLE"`) {
			t.Fatalf("%s %s: body = %s, want UNAVAILABLE envelope", req.Method, req.URL.Path, rec.Body.String())
		}
	}

	// Then: pulls (GET catalog) are still served during the collection.
	rec := httptest.NewRecorder()
	v2.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v2/_catalog", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v2/_catalog: status = %d, want 200", rec.Code)
	}
}

func Test_v2Writes_when_noGC_then_not503(t *testing.T) {
	// Given: the /v2 tree with the quiesce guard and no collection running.
	q := gcTestDB(t)
	store := newGCTestStore()
	v2 := api.NewRouter(store, q, api.WithWriteQuiesce(gc.NewLock().WriteGuard))

	// When: a write arrives with the lock idle.
	rec := httptest.NewRecorder()
	v2.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/v2/app/base/manifests/latest", nil))

	// Then: the request reaches the handler (here: schema validation refuses
	// the empty body) instead of being quiesced.
	if rec.Code == http.StatusServiceUnavailable {
		t.Fatal("idle guard returned 503 for a write")
	}
}
