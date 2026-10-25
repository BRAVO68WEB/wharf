package api_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/user/better-docker-registry/internal/db"
	"github.com/user/better-docker-registry/internal/storage"
)

// fakeStorage is an in-memory storage.Storage test double. It buffers blobs
// because a test double has no I/O to stream around; the handlers under test
// still stream (they copy out of the reader instead of reading it all).
type fakeStorage struct {
	mu    sync.Mutex
	blobs map[string][]byte
	fail  error // when set, every call fails with it
}

// Compile-time proof that the fake satisfies the interface the handlers use.
var _ storage.Storage = (*fakeStorage)(nil)

func newFakeStorage() *fakeStorage {
	return &fakeStorage{blobs: make(map[string][]byte)}
}

// seedBlobRef records that repoName uses digest. The repository row is
// created when it does not exist yet.
func seedBlobRef(t *testing.T, q db.Querier, repoName, digest string) {
	t.Helper()
	repo, err := q.GetRepositoryByName(t.Context(), repoName)
	if errors.Is(err, sql.ErrNoRows) {
		repo, err = q.CreateRepository(t.Context(), repoName)
	}
	if err != nil {
		t.Fatalf("repository %s: %v", repoName, err)
	}
	if err := q.AddBlobRef(t.Context(), db.AddBlobRefParams{Digest: digest, RepoID: repo.ID}); err != nil {
		t.Fatalf("AddBlobRef %s %s: %v", repoName, digest, err)
	}
}

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// seed stores data under its own digest and returns that digest.
func (f *fakeStorage) seed(data []byte) string {
	d := digestOf(data)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blobs[d] = data
	return d
}

func (f *fakeStorage) Get(ctx context.Context, digest string) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return nil, f.fail
	}
	b, ok := f.blobs[digest]
	if !ok {
		return nil, fmt.Errorf("get %s: %w", digest, storage.ErrBlobNotFound)
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (f *fakeStorage) Put(ctx context.Context, digest string, r io.Reader) (int64, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return 0, err
	}
	if got := digestOf(b); got != digest {
		return int64(len(b)), fmt.Errorf("put %s: %w (content hashes to %s)", digest, storage.ErrBlobInvalid, got)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blobs[digest] = b
	return int64(len(b)), nil
}

func (f *fakeStorage) Exists(ctx context.Context, digest string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return false, f.fail
	}
	_, ok := f.blobs[digest]
	return ok, nil
}

func (f *fakeStorage) Delete(ctx context.Context, digest string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	if _, ok := f.blobs[digest]; !ok {
		return fmt.Errorf("delete %s: %w", digest, storage.ErrBlobNotFound)
	}
	delete(f.blobs, digest)
	return nil
}

func (f *fakeStorage) Size(ctx context.Context, digest string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return 0, f.fail
	}
	b, ok := f.blobs[digest]
	if !ok {
		return 0, fmt.Errorf("size %s: %w", digest, storage.ErrBlobNotFound)
	}
	return int64(len(b)), nil
}

func (f *fakeStorage) List(ctx context.Context, fn func(digest string) error) error {
	f.mu.Lock()
	digests := make([]string, 0, len(f.blobs))
	for d := range f.blobs {
		digests = append(digests, d)
	}
	f.mu.Unlock()
	for _, d := range digests {
		if err := fn(d); err != nil {
			return err
		}
	}
	return nil
}

// serve runs one request through the registry route tree.
func serve(t *testing.T, h http.Handler, method, path string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func assertHeader(t *testing.T, h http.Header, key, want string) {
	t.Helper()
	if got := h.Get(key); got != want {
		t.Fatalf("header %s = %q, want %q", key, got, want)
	}
}

// decodeErrors decodes the registry error contract body.
func decodeErrors(t *testing.T, rec *httptest.ResponseRecorder) *storage.ErrorResponse {
	t.Helper()
	var body storage.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	if len(body.Errors) != 1 {
		t.Fatalf("error body has %d entries, want 1: %s", len(body.Errors), rec.Body.String())
	}
	return &body
}
