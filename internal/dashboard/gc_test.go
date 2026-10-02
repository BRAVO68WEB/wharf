package dashboard_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/user/better-docker-registry/internal/dashboard"
	"github.com/user/better-docker-registry/internal/db"
	"github.com/user/better-docker-registry/internal/gc"
	"github.com/user/better-docker-registry/internal/storage"
)

// GC UI tests (plan task 18): stats page, dry-run listing, real sweep, CSRF
// and the /v2 quiesce wiring. Helpers are gcTest-prefixed — dashboard_test is
// shared with the repo/tag/manifest suites.

// gcTestStore is a map-backed storage.Storage. Delete keeps the task-4
// contract (missing digest fails with ErrBlobNotFound) so the sweep runs
// against real driver semantics; the driver itself is task 5's to test.
// mu guards blobs: gc.Run sweeps with parallel delete workers (gc_sweep.go),
// and real drivers are safe for concurrent Delete — the fake must be too.
type gcTestStore struct {
	mu    sync.Mutex
	blobs map[string][]byte
}

func newGCTestStore() *gcTestStore {
	return &gcTestStore{blobs: make(map[string][]byte)}
}

func (s *gcTestStore) blobCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.blobs)
}

func (s *gcTestStore) hasBlob(digest string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.blobs[digest]
	return ok
}

func (s *gcTestStore) Get(_ context.Context, digest string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	content, ok := s.blobs[digest]
	if !ok {
		return nil, fmt.Errorf("gcTestStore get %s: %w", digest, storage.ErrBlobNotFound)
	}
	return io.NopCloser(bytes.NewReader(content)), nil
}

func (s *gcTestStore) Put(_ context.Context, digest string, r io.Reader) (int64, error) {
	content, err := io.ReadAll(r)
	if err != nil {
		return 0, err
	}
	if got := fmt.Sprintf("sha256:%x", sha256.Sum256(content)); got != digest {
		return 0, fmt.Errorf("gcTestStore put %s: %w", digest, storage.ErrBlobInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blobs[digest] = content
	return int64(len(content)), nil
}

func (s *gcTestStore) Exists(_ context.Context, digest string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.blobs[digest]
	return ok, nil
}

func (s *gcTestStore) Delete(_ context.Context, digest string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.blobs[digest]; !ok {
		return fmt.Errorf("gcTestStore delete %s: %w", digest, storage.ErrBlobNotFound)
	}
	delete(s.blobs, digest)
	return nil
}

func (s *gcTestStore) Size(_ context.Context, digest string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	content, ok := s.blobs[digest]
	if !ok {
		return 0, fmt.Errorf("gcTestStore size %s: %w", digest, storage.ErrBlobNotFound)
	}
	return int64(len(content)), nil
}

func (s *gcTestStore) List(_ context.Context, fn func(digest string) error) error {
	s.mu.Lock()
	digests := make([]string, 0, len(s.blobs))
	for digest := range s.blobs {
		digests = append(digests, digest)
	}
	s.mu.Unlock()
	for _, digest := range digests {
		if err := fn(digest); err != nil {
			return err
		}
	}
	return nil
}

// gcTestDB opens and migrates a throwaway database.
func gcTestDB(t *testing.T) db.Querier {
	t.Helper()
	sqlDB, err := db.OpenAndMigrate(t.Context(), filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db.New(sqlDB)
}

// gcTestPut stores content and returns its real sha256 digest.
func gcTestPut(t *testing.T, store storage.Storage, content string) string {
	t.Helper()
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(content)))
	if _, err := store.Put(t.Context(), digest, bytes.NewBufferString(content)); err != nil {
		t.Fatalf("Put %s: %v", digest, err)
	}
	return digest
}

// gcTestSeed is the shared fixture: 5 blobs, a manifest referencing 2 of
// them, so exactly 3 orphans are collectable.
type gcTestSeed struct {
	Config, Layer                string
	Orphans                      []string
	ReferencedBytes, OrphanBytes int64
}

func gcTestSeedFive(t *testing.T, q db.Querier, store storage.Storage) gcTestSeed {
	t.Helper()
	ctx := t.Context()
	repo, err := q.CreateRepository(ctx, "app/base")
	if err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	seed := gcTestSeed{
		Config: gcTestPut(t, store, "config-bytes"),
		Layer:  gcTestPut(t, store, "layer-bytes"),
		Orphans: []string{
			gcTestPut(t, store, "orphan-one"),
			gcTestPut(t, store, "orphan-two"),
			gcTestPut(t, store, "orphan-three"),
		},
	}
	seed.ReferencedBytes = int64(len("config-bytes") + len("layer-bytes"))
	seed.OrphanBytes = int64(len("orphan-one") + len("orphan-two") + len("orphan-three"))

	payload := fmt.Sprintf(
		`{"schemaVersion":2,"config":{"digest":%q,"size":1},"layers":[{"digest":%q,"size":1}]}`,
		seed.Config, seed.Layer,
	)
	err = q.UpsertManifest(ctx, db.UpsertManifestParams{
		RepoID:    repo.ID,
		Digest:    fmt.Sprintf("sha256:%064x", 1),
		MediaType: "application/vnd.oci.image.manifest.v1+json",
		Payload:   []byte(payload),
		Size:      int64(len(payload)),
	})
	if err != nil {
		t.Fatalf("UpsertManifest: %v", err)
	}
	return seed
}

// gcTestRouter mounts the GC routes over a fresh database and store.
func gcTestRouter(t *testing.T, lock *gc.Lock) (http.Handler, db.Querier, *gcTestStore) {
	t.Helper()
	q := gcTestDB(t)
	store := newGCTestStore()
	r := chi.NewRouter()
	dashboard.NewGCHandler(q, store, lock).Mount(r)
	return r, q, store
}

// gcTestRequest issues one request with HTMX swap headers and returns the
// recorded response.
func gcTestRequest(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func Test_servePage_when_rendered_then_statsAndButtons(t *testing.T) {
	// Given: 5 blobs of which 2 are referenced by a manifest.
	lock := gc.NewLock()
	h, q, store := gcTestRouter(t, lock)
	seed := gcTestSeedFive(t, q, store)

	// When: the GC page is fetched.
	rec := gcTestRequest(t, h, http.MethodGet, "/dashboard/gc")

	// Then: the summary numbers are on the page.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`data-testid="gc-stats"`,
		`data-testid="gc-total-blobs">5<`,
		`data-testid="gc-referenced-blobs">2<`,
		`data-testid="gc-unreferenced-blobs">3<`,
		fmt.Sprintf(`data-testid="gc-unreferenced-bytes">%s<`, dashboard.FormatBytes(seed.OrphanBytes)),
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("page missing %q", want)
		}
	}

	// And: both action buttons exist with the plan's test ids and HTMX posts.
	for _, want := range []string{
		`data-testid="gc-dry-run-btn"`,
		`data-testid="gc-run-btn"`,
		`hx-post="/dashboard/gc/dry-run"`,
		`hx-post="/dashboard/gc/run"`,
		`hx-swap="innerHTML"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("page missing %q", want)
		}
	}

	// And: the run button carries the confirmation dialog with live counts.
	confirm := fmt.Sprintf("This will delete 3 blobs, total %s. Continue?", dashboard.FormatBytes(seed.OrphanBytes))
	if !strings.Contains(body, confirm) {
		t.Fatalf("page missing confirm dialog %q", confirm)
	}
}

func Test_serveDryRun_when_posted_then_listsExactlyUnreferenced(t *testing.T) {
	// Given: the standard 5-blob fixture.
	lock := gc.NewLock()
	h, q, store := gcTestRouter(t, lock)
	seed := gcTestSeedFive(t, q, store)

	// When: the dry-run button posts.
	rec := gcTestRequest(t, h, http.MethodPost, "/dashboard/gc/dry-run")

	// Then: exactly the 3 orphan digests are listed with sizes.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if n := strings.Count(body, `data-testid="gc-result-row"`); n != 3 {
		t.Fatalf("listed %d rows, want 3", n)
	}
	for _, orphan := range seed.Orphans {
		if !strings.Contains(body, orphan) {
			t.Fatalf("dry-run listing missing %s", orphan)
		}
	}
	for _, live := range []string{seed.Config, seed.Layer} {
		if strings.Contains(body, live) {
			t.Fatalf("dry-run listing leaked referenced blob %s", live)
		}
	}
	if !strings.Contains(body, "would be deleted") {
		t.Fatalf("missing dry-run heading in %q", body)
	}

	// And: nothing was deleted.
	if n := store.blobCount(); n != 5 {
		t.Fatalf("store holds %d blobs after dry run, want 5", n)
	}

	// And: the fragment has no page shell (it swaps into #gc-results).
	// templ normalises the doctype to lowercase in rendered output.
	if strings.Contains(body, "<!doctype html>") {
		t.Fatal("HTMX dry-run response must be a fragment, not a full page")
	}
}
