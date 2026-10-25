package storage_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/BRAVO68WEB/wharf/internal/storage"
)

// memStorage is an in-memory storage.Storage fake. It is a test double that
// pins the behavioural contract the filesystem and S3 drivers must satisfy —
// not a driver itself. It buffers blobs because a test double has no I/O cost
// to stream around; drivers must stream.
type memStorage struct {
	blobs map[string][]byte
}

func newMemStorage() *memStorage {
	return &memStorage{blobs: make(map[string][]byte)}
}

// Compile-time proof that the fake satisfies the interface under test.
var _ storage.Storage = (*memStorage)(nil)

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (m *memStorage) Get(ctx context.Context, digest string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b, ok := m.blobs[digest]
	if !ok {
		return nil, fmt.Errorf("get %s: %w", digest, storage.ErrBlobNotFound)
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (m *memStorage) Put(ctx context.Context, digest string, r io.Reader) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return 0, err
	}
	if got := digestOf(b); got != digest {
		return int64(len(b)), fmt.Errorf("put %s: %w (content hashes to %s)", digest, storage.ErrBlobInvalid, got)
	}
	m.blobs[digest] = b
	return int64(len(b)), nil
}

func (m *memStorage) Exists(ctx context.Context, digest string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	_, ok := m.blobs[digest]
	return ok, nil
}

func (m *memStorage) Delete(ctx context.Context, digest string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := m.blobs[digest]; !ok {
		return fmt.Errorf("delete %s: %w", digest, storage.ErrBlobNotFound)
	}
	delete(m.blobs, digest)
	return nil
}

func (m *memStorage) Size(ctx context.Context, digest string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	b, ok := m.blobs[digest]
	if !ok {
		return 0, fmt.Errorf("size %s: %w", digest, storage.ErrBlobNotFound)
	}
	return int64(len(b)), nil
}

func (m *memStorage) List(ctx context.Context, fn func(digest string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for d := range m.blobs {
		if err := fn(d); err != nil {
			return err
		}
	}
	return nil
}

// Given: a fresh in-memory fake
// When:  it is used through the storage.Storage interface
// Then:  Put/Get dispatch across the interface and round-trip the body
func Test_InterfaceCompliance_when_MockUsedAsStorage(t *testing.T) {
	var s storage.Storage = newMemStorage()
	ctx := context.Background()
	body := []byte("hello registry")
	digest := digestOf(body)

	n, err := s.Put(ctx, digest, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if n != int64(len(body)) {
		t.Fatalf("Put count = %d, want %d", n, len(body))
	}

	rc, err := s.Get(ctx, digest)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("Get body = %q, want %q", got, body)
	}
}

// Given: an empty store
// When:  Get is called with an unknown digest
// Then:  it fails with a wrap of ErrBlobNotFound
func Test_Get_when_DigestMissing_then_ErrBlobNotFound(t *testing.T) {
	s := newMemStorage()
	_, err := s.Get(context.Background(), digestOf([]byte("missing")))
	if !errors.Is(err, storage.ErrBlobNotFound) {
		t.Fatalf("Get err = %v, want ErrBlobNotFound", err)
	}
}

// Given: a reader whose content hashes to a different digest
// When:  Put is called with the mismatching digest
// Then:  it fails with a wrap of ErrBlobInvalid
func Test_Put_when_DigestMismatch_then_ErrBlobInvalid(t *testing.T) {
	s := newMemStorage()
	_, err := s.Put(context.Background(), digestOf([]byte("expected")), bytes.NewReader([]byte("actual")))
	if !errors.Is(err, storage.ErrBlobInvalid) {
		t.Fatalf("Put err = %v, want ErrBlobInvalid", err)
	}
}

// Given: an empty store and a store holding one blob
// When:  Exists is called
// Then:  it reports false and true respectively without an error
func Test_Exists_when_DigestMissingOrPresent_then_ReportsPresence(t *testing.T) {
	s := newMemStorage()
	ctx := context.Background()
	body := []byte("present")
	digest := digestOf(body)

	got, err := s.Exists(ctx, digest)
	if err != nil || got {
		t.Fatalf("Exists(missing) = %v, %v; want false, nil", got, err)
	}

	if _, err := s.Put(ctx, digest, bytes.NewReader(body)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err = s.Exists(ctx, digest)
	if err != nil || !got {
		t.Fatalf("Exists(present) = %v, %v; want true, nil", got, err)
	}
}

// Given: an empty store
// When:  Delete is called with an unknown digest
// Then:  it fails with a wrap of ErrBlobNotFound
func Test_Delete_when_DigestMissing_then_ErrBlobNotFound(t *testing.T) {
	s := newMemStorage()
	err := s.Delete(context.Background(), digestOf([]byte("missing")))
	if !errors.Is(err, storage.ErrBlobNotFound) {
		t.Fatalf("Delete err = %v, want ErrBlobNotFound", err)
	}
}

// Given: a store holding one blob
// When:  Delete is called with that digest
// Then:  the blob is gone from the store
func Test_Delete_when_DigestPresent_then_RemovesBlob(t *testing.T) {
	s := newMemStorage()
	ctx := context.Background()
	body := []byte("doomed")
	digest := digestOf(body)

	if _, err := s.Put(ctx, digest, bytes.NewReader(body)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Delete(ctx, digest); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, err := s.Exists(ctx, digest)
	if err != nil || got {
		t.Fatalf("Exists after Delete = %v, %v; want false, nil", got, err)
	}
}

// Given: an empty store and a store holding one blob
// When:  Size is called
// Then:  it fails with ErrBlobNotFound and returns the byte length respectively
func Test_Size_when_DigestMissingOrPresent_then_ErrBlobNotFoundOrLength(t *testing.T) {
	s := newMemStorage()
	ctx := context.Background()
	body := []byte("0123456789")
	digest := digestOf(body)

	if _, err := s.Size(ctx, digest); !errors.Is(err, storage.ErrBlobNotFound) {
		t.Fatalf("Size(missing) err = %v, want ErrBlobNotFound", err)
	}

	if _, err := s.Put(ctx, digest, bytes.NewReader(body)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	n, err := s.Size(ctx, digest)
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if n != int64(len(body)) {
		t.Fatalf("Size = %d, want %d", n, len(body))
	}
}

// Given: a store holding three blobs
// When:  List is called with a collecting callback
// Then:  every digest is visited exactly once
func Test_List_when_StorePopulated_then_VisitsEveryDigest(t *testing.T) {
	s := newMemStorage()
	ctx := context.Background()
	want := map[string]bool{}
	for _, body := range [][]byte{[]byte("a"), []byte("bb"), []byte("ccc")} {
		d := digestOf(body)
		want[d] = true
		if _, err := s.Put(ctx, d, bytes.NewReader(body)); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}

	seen := map[string]int{}
	if err := s.List(ctx, func(digest string) error {
		seen[digest]++
		return nil
	}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(seen) != len(want) {
		t.Fatalf("List visited %d digests, want %d", len(seen), len(want))
	}
	for d := range want {
		if seen[d] != 1 {
			t.Fatalf("digest %s visited %d times, want 1", d, seen[d])
		}
	}
}

// Given: a store holding three blobs
// When:  List's callback returns an error on the first call
// Then:  iteration stops immediately and the callback's error is propagated
func Test_List_when_CallbackErrors_then_StopsAndPropagates(t *testing.T) {
	s := newMemStorage()
	ctx := context.Background()
	for _, body := range [][]byte{[]byte("a"), []byte("bb"), []byte("ccc")} {
		if _, err := s.Put(ctx, digestOf(body), bytes.NewReader(body)); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}

	stop := errors.New("stop")
	visits := 0
	err := s.List(ctx, func(string) error {
		visits++
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatalf("List err = %v, want %v", err, stop)
	}
	if visits != 1 {
		t.Fatalf("callback invoked %d times, want 1", visits)
	}
}

// Given: a canceled context
// When:  a storage operation runs
// Then:  it fails with the context's error instead of doing work
func Test_Operations_when_ContextCanceled_then_ReturnCtxError(t *testing.T) {
	s := newMemStorage()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := s.Get(ctx, digestOf([]byte("x"))); !errors.Is(err, context.Canceled) {
		t.Fatalf("Get err = %v, want context.Canceled", err)
	}
	if _, err := s.Put(ctx, digestOf([]byte("x")), bytes.NewReader([]byte("x"))); !errors.Is(err, context.Canceled) {
		t.Fatalf("Put err = %v, want context.Canceled", err)
	}
}
