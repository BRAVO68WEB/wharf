package s3_test

// Error-path contract tests: every failure the storage.Storage contract
// prescribes must surface as a wrap of the matching sentinel.

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/user/better-docker-registry/internal/storage"
)

// Given: a store that does not hold the digest
// When:  Get is called
// Then:  it fails with a wrap of ErrBlobNotFound
func Test_Get_when_DigestMissing_then_ErrBlobNotFound(t *testing.T) {
	store := newStore(t)
	digest := digestOf([]byte("missing"))
	_, err := store.Get(context.Background(), digest)
	if !errors.Is(err, storage.ErrBlobNotFound) {
		t.Fatalf("Get err = %v, want ErrBlobNotFound", err)
	}
	if !strings.Contains(err.Error(), digest) {
		t.Fatalf("Get err = %q, want it to name the digest %q", err, digest)
	}
}

// Given: a store that does not hold the digest
// When:  Size is called
// Then:  it fails with a wrap of ErrBlobNotFound
func Test_Size_when_DigestMissing_then_ErrBlobNotFound(t *testing.T) {
	store := newStore(t)
	if _, err := store.Size(context.Background(), digestOf([]byte("missing"))); !errors.Is(err, storage.ErrBlobNotFound) {
		t.Fatalf("Size err = %v, want ErrBlobNotFound", err)
	}
}

// Given: a store that does not hold the digest
// When:  Delete is called
// Then:  it fails with a wrap of ErrBlobNotFound, so callers can tell "never
//
//	existed" from "now gone"
func Test_Delete_when_DigestMissing_then_ErrBlobNotFound(t *testing.T) {
	store := newStore(t)
	err := store.Delete(context.Background(), digestOf([]byte("missing")))
	if !errors.Is(err, storage.ErrBlobNotFound) {
		t.Fatalf("Delete err = %v, want ErrBlobNotFound", err)
	}
}

// Given: a store holding one blob
// When:  Delete is called with that digest
// Then:  the blob is gone and a second Delete reports ErrBlobNotFound
func Test_Delete_when_DigestPresent_then_RemovesBlob(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	body, digest := blobOf(64)

	if _, err := store.Put(ctx, digest, bytes.NewReader(body)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := store.Delete(ctx, digest); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err := store.Exists(ctx, digest)
	if err != nil || got {
		t.Fatalf("Exists after Delete = %v, %v; want false, nil", got, err)
	}
	if err := store.Delete(ctx, digest); !errors.Is(err, storage.ErrBlobNotFound) {
		t.Fatalf("second Delete err = %v, want ErrBlobNotFound", err)
	}
}

// Given: a reader whose content hashes to a different digest
// When:  Put is called with the mismatching digest
// Then:  it fails with ErrBlobInvalid and leaves no readable blob behind
func Test_Put_when_DigestMismatch_then_ErrBlobInvalidAndNoBlob(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	claimed := digestOf([]byte("expected"))
	actual := []byte("actual content that hashes elsewhere")

	_, err := store.Put(ctx, claimed, bytes.NewReader(actual))
	if !errors.Is(err, storage.ErrBlobInvalid) {
		t.Fatalf("Put err = %v, want ErrBlobInvalid", err)
	}

	// The contract: a failed Put must not leave a readable blob behind.
	got, err := store.Exists(ctx, claimed)
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if got {
		t.Fatal("Exists after failed Put = true, want false")
	}
	if _, err := store.Get(ctx, claimed); !errors.Is(err, storage.ErrBlobNotFound) {
		t.Fatalf("Get after failed Put err = %v, want ErrBlobNotFound", err)
	}
}

// Given: a store and a reader that fails mid-stream
// When:  Put is called
// Then:  it fails and does not report a byte count as if the store succeeded
func Test_Put_when_BodyReadFails_then_Error(t *testing.T) {
	store := newStore(t)
	boom := errors.New("source exploded")

	_, err := store.Put(context.Background(), digestOf([]byte("never")), &ioErrAfter{after: 8, err: boom})
	if err == nil {
		t.Fatal("Put err = nil, want failure")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("Put err = %v, want it to match the body read error", err)
	}
}

// ioErrAfter yields after bytes of "x" and then fails with err.
type ioErrAfter struct {
	after int
	err   error
	read  int
}

func (r *ioErrAfter) Read(p []byte) (int, error) {
	if r.read >= r.after {
		return 0, r.err
	}
	n := len(p)
	if remain := r.after - r.read; n > remain {
		n = remain
	}
	for i := 0; i < n; i++ {
		p[i] = 'x'
	}
	r.read += n
	return n, nil
}
