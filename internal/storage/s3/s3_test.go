package s3_test

// Contract tests for the S3 driver's happy path against a live MinIO server.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"sync"
	"testing"

	"github.com/BRAVO68WEB/wharf/internal/storage"
	s3store "github.com/BRAVO68WEB/wharf/internal/storage/s3"
)

// Compile-time proof that the driver honours the interface under contract.
var _ storage.Storage = (*s3store.Store)(nil)

// digestOf returns the OCI digest of b.
func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// blobOf returns n pseudo-random bytes with their digest.
func blobOf(n int) ([]byte, string) {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*31 + 7)
	}
	return b, digestOf(b)
}

// Given: a store and a 5MB blob
// When:  Put writes it and Get reads it back
// Then:  the bytes are identical and Size agrees with the length
func Test_Roundtrip_when_PutThenGet_then_IdenticalBytes(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	body, digest := blobOf(5 << 20)

	n, err := store.Put(ctx, digest, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if n != int64(len(body)) {
		t.Fatalf("Put count = %d, want %d", n, len(body))
	}

	rc, err := store.Get(ctx, digest)
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
		t.Fatalf("Get body length = %d, want %d (content differs)", len(got), len(body))
	}

	size, err := store.Size(ctx, digest)
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if size != int64(len(body)) {
		t.Fatalf("Size = %d, want %d", size, len(body))
	}
}

// Given: a store and a zero-length blob
// When:  Put writes it and Get reads it back
// Then:  the round-trip succeeds and Size is 0
func Test_Roundtrip_when_EmptyBlob_then_Roundtrips(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	body := []byte{}
	digest := digestOf(body)

	n, err := store.Put(ctx, digest, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if n != 0 {
		t.Fatalf("Put count = %d, want 0", n)
	}

	rc, err := store.Get(ctx, digest)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	rc.Close()
	if len(got) != 0 {
		t.Fatalf("Get body = %q, want empty", got)
	}
}

// Given: an empty store and a store holding one blob
// When:  Exists is called
// Then:  it reports false and true respectively without an error
func Test_Exists_when_DigestMissingOrPresent_then_ReportsPresence(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	body, digest := blobOf(32)

	got, err := store.Exists(ctx, digest)
	if err != nil || got {
		t.Fatalf("Exists(missing) = %v, %v; want false, nil", got, err)
	}

	if _, err := store.Put(ctx, digest, bytes.NewReader(body)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err = store.Exists(ctx, digest)
	if err != nil || !got {
		t.Fatalf("Exists(present) = %v, %v; want true, nil", got, err)
	}
}

// Given: a store holding one blob
// When:  Put and Get run concurrently from many goroutines
// Then:  every write and read succeeds and the race detector stays quiet
func Test_Roundtrip_when_Concurrent_then_RaceFree(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	const workers = 8
	bodies := make([][]byte, workers)
	digests := make([]string, workers)
	for i := range bodies {
		bodies[i], digests[i] = blobOf(4096 + i*17)
	}

	var wg sync.WaitGroup
	for i := range bodies {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := store.Put(ctx, digests[i], bytes.NewReader(bodies[i])); err != nil {
				t.Errorf("Put[%d]: %v", i, err)
			}
		}()
	}
	wg.Wait()

	for i := range bodies {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rc, err := store.Get(ctx, digests[i])
			if err != nil {
				t.Errorf("Get[%d]: %v", i, err)
				return
			}
			got, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				t.Errorf("read[%d]: %v", i, err)
				return
			}
			if !bytes.Equal(got, bodies[i]) {
				t.Errorf("Get[%d] body differs", i)
			}
		}()
	}
	wg.Wait()
}
