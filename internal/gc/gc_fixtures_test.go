package gc_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BRAVO68WEB/wharf/internal/db"
	"github.com/BRAVO68WEB/wharf/internal/storage"
)

// Engine tests use a real temp SQLite (db.OpenAndMigrate) and a map-backed
// storage fake: the driver is not under test here (task 5 owns that), but
// every query against the live schema is.

// gcTestStore is an in-memory storage.Storage. Delete mirrors the task-4
// contract (non-idempotent: missing digest fails with ErrBlobNotFound) so the
// sweep is exercised against the same semantics the real drivers have. The
// mutex holds because the contract is concurrency-safe and the sweep issues
// one batch of deletes at a time in parallel.
type gcTestStore struct {
	mu    sync.Mutex
	blobs map[string][]byte
}

func newGCTestStore() *gcTestStore {
	return &gcTestStore{blobs: make(map[string][]byte)}
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
		return 0, fmt.Errorf("gcTestStore put %s: %w (content hashes to %s)", digest, storage.ErrBlobInvalid, got)
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
	for d := range s.blobs {
		digests = append(digests, d)
	}
	s.mu.Unlock()
	slices.Sort(digests)
	for _, d := range digests {
		if err := fn(d); err != nil {
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

// gcTestManifest records a manifest whose payload references the given
// config/layer digests.
func gcTestManifest(t *testing.T, q db.Querier, repoID int64, digest, payload string) {
	t.Helper()
	err := q.UpsertManifest(t.Context(), db.UpsertManifestParams{
		RepoID:    repoID,
		Digest:    digest,
		MediaType: "application/vnd.oci.image.manifest.v1+json",
		Payload:   []byte(payload),
		Size:      int64(len(payload)),
	})
	if err != nil {
		t.Fatalf("UpsertManifest %s: %v", digest, err)
	}
}

// gcTestUploadSeq keeps upload session uuids unique across a test run.
var gcTestUploadSeq atomic.Int64

// gcTestUpload records an upload session claiming digest until expiresAt
// (unix seconds).
func gcTestUpload(t *testing.T, q db.Querier, repoID int64, digest string, expiresAt int64) {
	t.Helper()
	err := q.CreateUpload(t.Context(), db.CreateUploadParams{
		Uuid:      fmt.Sprintf("upload-%d", gcTestUploadSeq.Add(1)),
		RepoID:    repoID,
		Digest:    digest,
		Offset:    0,
		Size:      1,
		TempPath:  filepath.Join(t.TempDir(), "chunk"),
		CreatedAt: time.Now().Unix(),
		ExpiresAt: expiresAt,
	})
	if err != nil {
		t.Fatalf("CreateUpload %s: %v", digest, err)
	}
}

// gcTestDigest builds a syntactically shaped sha256 digest for fixtures.
func gcTestDigest(n int) string {
	return fmt.Sprintf("sha256:%064x", n)
}

// gcTestManifestPayload renders a minimal image manifest referencing config
// and layer digests.
func gcTestManifestPayload(config string, layers ...string) string {
	payload := fmt.Sprintf(`{"schemaVersion":2,"config":{"digest":%q,"size":1},"layers":[`, config)
	for i, l := range layers {
		if i > 0 {
			payload += ","
		}
		payload += fmt.Sprintf(`{"digest":%q,"size":1}`, l)
	}
	return payload + "]}"
}
