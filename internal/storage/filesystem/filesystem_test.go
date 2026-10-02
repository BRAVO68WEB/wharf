// Package filesystem_test exercises the filesystem blob store against the
// storage.Storage contract. It uses the real filesystem (t.TempDir) rather
// than fakes: the driver is pure I/O and a temp dir is faster and truer than
// a mock.
package filesystem_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/user/better-docker-registry/internal/storage"
)

// Given: an empty store
// When:  a 5 MiB blob is Put and read back with Get
// Then:  the bytes round-trip exactly, Size matches, and the file lands in the layout
func Test_PutGet_when_FiveMegabyteBlob_then_RoundTripsWithSize(t *testing.T) {
	s, root := newStore(t, 0)
	ctx := context.Background()
	body := makeBlob(5 << 20)
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
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("Get body = %d bytes, want %d (content differs)", len(got), len(body))
	}

	size, err := s.Size(ctx, digest)
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if size != int64(len(body)) {
		t.Fatalf("Size = %d, want %d", size, len(body))
	}

	if _, err := os.Stat(blobPath(root, digest)); err != nil {
		t.Fatalf("blob file at layout path: %v", err)
	}
	assertNoTempFiles(t, root)
}

// Given: a store holding one blob
// When:  Exists and Size are called for it and for an unknown digest
// Then:  presence is reported truthfully and absence fails with ErrBlobNotFound
func Test_Presence_when_PresentOrMissing_then_ExistsAndSizeBehave(t *testing.T) {
	s, _ := newStore(t, 0)
	ctx := context.Background()
	body := []byte("0123456789")
	digest := digestOf(body)
	missing := digestOf([]byte("missing"))

	ok, err := s.Exists(ctx, missing)
	if err != nil || ok {
		t.Fatalf("Exists(missing) = %v, %v; want false, nil", ok, err)
	}
	if _, err := s.Size(ctx, missing); !errors.Is(err, storage.ErrBlobNotFound) {
		t.Fatalf("Size(missing) err = %v, want ErrBlobNotFound", err)
	}
	if _, err := s.Get(ctx, missing); !errors.Is(err, storage.ErrBlobNotFound) {
		t.Fatalf("Get(missing) err = %v, want ErrBlobNotFound", err)
	}

	if _, err := s.Put(ctx, digest, bytes.NewReader(body)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	ok, err = s.Exists(ctx, digest)
	if err != nil || !ok {
		t.Fatalf("Exists(present) = %v, %v; want true, nil", ok, err)
	}
	size, err := s.Size(ctx, digest)
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if size != int64(len(body)) {
		t.Fatalf("Size = %d, want %d", size, len(body))
	}
}

// Given: a reader whose content hashes to a different digest
// When:  Put is called with the mismatching digest
// Then:  it fails with ErrBlobInvalid and leaves no readable blob behind
func Test_Put_when_DigestMismatch_then_ErrBlobInvalidAndNoBlob(t *testing.T) {
	s, root := newStore(t, 0)
	ctx := context.Background()
	body := []byte("actual content")
	digest := digestOf([]byte("different content"))

	n, err := s.Put(ctx, digest, bytes.NewReader(body))
	if !errors.Is(err, storage.ErrBlobInvalid) {
		t.Fatalf("Put err = %v, want ErrBlobInvalid", err)
	}
	if n != int64(len(body)) {
		t.Fatalf("Put count = %d, want %d", n, len(body))
	}
	if ok, err := s.Exists(ctx, digest); err != nil || ok {
		t.Fatalf("Exists after bad Put = %v, %v; want false, nil", ok, err)
	}
	assertNoFiles(t, root)
	assertNoTempFiles(t, root)
}

// Given: a store holding one blob
// When:  Delete is called for it
// Then:  the file and its now-empty prefix directory are gone, and a second Delete reports gone
func Test_Delete_when_SoleBlobInPrefixDir_then_RemovesFileAndPrefixDir(t *testing.T) {
	s, root := newStore(t, 0)
	ctx := context.Background()
	body := []byte("doomed")
	digest := digestOf(body)

	if _, err := s.Put(ctx, digest, bytes.NewReader(body)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Delete(ctx, digest); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := os.Stat(blobPath(root, digest)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("blob file stat err = %v, want not-exist", err)
	}
	if _, err := os.Stat(filepath.Join(root, digest[:2])); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("prefix dir stat err = %v, want not-exist", err)
	}
	// Delete is deliberately not idempotent: "gone" must be tellable from
	// "never existed".
	if err := s.Delete(ctx, digest); !errors.Is(err, storage.ErrBlobNotFound) {
		t.Fatalf("second Delete err = %v, want ErrBlobNotFound", err)
	}
}

// Given: two blobs sharing one prefix directory
// When:  one of them is deleted
// Then:  the sibling stays readable and the shared prefix directory remains
func Test_Delete_when_SiblingSharesPrefixDir_then_KeepsSiblingAndDir(t *testing.T) {
	s, root := newStore(t, 0)
	ctx := context.Background()
	// Every sha256 digest shares the "sh" prefix directory.
	a, b := []byte("first blob"), []byte("second blob")
	digestA, digestB := digestOf(a), digestOf(b)

	for _, d := range []struct {
		body   []byte
		digest string
	}{{a, digestA}, {b, digestB}} {
		if _, err := s.Put(ctx, d.digest, bytes.NewReader(d.body)); err != nil {
			t.Fatalf("Put %s: %v", d.digest, err)
		}
	}

	if err := s.Delete(ctx, digestA); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, digestB[:2])); err != nil {
		t.Fatalf("prefix dir after Delete: %v", err)
	}
	rc, err := s.Get(ctx, digestB)
	if err != nil {
		t.Fatalf("Get sibling: %v", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read sibling: %v", err)
	}
	_ = rc.Close()
	if !bytes.Equal(got, b) {
		t.Fatalf("sibling body = %q, want %q", got, b)
	}
}

// Given: a store configured with a maximum blob size
// When:  Put receives content at the limit and one byte over it
// Then:  the limit is accepted and the oversize write is rejected as unusable
func Test_Put_when_MaxBlobSizeConfigured_then_RejectsOversizedContent(t *testing.T) {
	s, root := newStore(t, 100)
	ctx := context.Background()

	over := makeBlob(101)
	overDigest := digestOf(over)
	_, err := s.Put(ctx, overDigest, bytes.NewReader(over))
	if !errors.Is(err, storage.ErrBlobInvalid) {
		t.Fatalf("Put oversize err = %v, want ErrBlobInvalid", err)
	}
	if ok, err := s.Exists(ctx, overDigest); err != nil || ok {
		t.Fatalf("Exists after oversize Put = %v, %v; want false, nil", ok, err)
	}
	assertNoFiles(t, root)

	at := makeBlob(100)
	atDigest := digestOf(at)
	n, err := s.Put(ctx, atDigest, bytes.NewReader(at))
	if err != nil {
		t.Fatalf("Put at limit: %v", err)
	}
	if n != 100 {
		t.Fatalf("Put count = %d, want 100", n)
	}
}

// Given: an empty store
// When:  a zero-byte blob is Put and read back
// Then:  it round-trips as an empty body with Size 0
func Test_PutGet_when_EmptyBlob_then_RoundTripsAsEmpty(t *testing.T) {
	s, _ := newStore(t, 0)
	ctx := context.Background()
	body := []byte{}
	digest := digestOf(body)

	n, err := s.Put(ctx, digest, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if n != 0 {
		t.Fatalf("Put count = %d, want 0", n)
	}
	rc, err := s.Get(ctx, digest)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	_ = rc.Close()
	if len(got) != 0 {
		t.Fatalf("Get body = %q, want empty", got)
	}
	size, err := s.Size(ctx, digest)
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if size != 0 {
		t.Fatalf("Size = %d, want 0", size)
	}
}
