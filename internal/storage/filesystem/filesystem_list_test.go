package filesystem_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/BRAVO68WEB/wharf/internal/storage"
)

// Given: a store holding three blobs plus stray non-blob clutter
// When:  List is called with a collecting callback
// Then:  exactly the three digests are visited, once each
func Test_List_when_StorePopulated_then_VisitsEveryDigest(t *testing.T) {
	s, root := newStore(t, 0)
	ctx := context.Background()
	want := map[string]bool{}
	for _, body := range [][]byte{[]byte("a"), []byte("bb"), []byte("ccc")} {
		d := digestOf(body)
		want[d] = true
		if _, err := s.Put(ctx, d, bytes.NewReader(body)); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}

	// Clutter the tree: an in-flight temp file, a stray root-level file, and
	// a deep directory — none of them are blobs.
	if err := os.WriteFile(filepath.Join(root, "sh", ".tmp-123"), []byte("partial"), 0o644); err != nil {
		t.Fatalf("plant temp file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "stray"), []byte("noise"), 0o644); err != nil {
		t.Fatalf("plant stray file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sh", "nested", "deeper"), 0o755); err != nil {
		t.Fatalf("plant nested dir: %v", err)
	}

	seen := map[string]int{}
	if err := s.List(ctx, func(digest string) error {
		seen[digest]++
		return nil
	}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(seen) != len(want) {
		t.Fatalf("List visited %d digests %v, want %d", len(seen), seen, len(want))
	}
	for d := range want {
		if seen[d] != 1 {
			t.Fatalf("digest %s visited %d times, want 1", d, seen[d])
		}
	}
}

// Given: an empty store
// When:  List is called
// Then:  the callback is never invoked
func Test_List_when_EmptyStore_then_NoInvocations(t *testing.T) {
	s, _ := newStore(t, 0)
	visits := 0
	if err := s.List(context.Background(), func(string) error {
		visits++
		return nil
	}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if visits != 0 {
		t.Fatalf("callback invoked %d times, want 0", visits)
	}
}

// Given: a store holding three blobs
// When:  List's callback returns an error on the first call
// Then:  iteration stops immediately and the error propagates unwrapped
func Test_List_when_CallbackErrors_then_StopsAndPropagates(t *testing.T) {
	s, _ := newStore(t, 0)
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
	if err != stop {
		t.Fatalf("List err = %v, want the callback's error unwrapped", err)
	}
	if visits != 1 {
		t.Fatalf("callback invoked %d times, want 1", visits)
	}
}

// Given: a canceled context
// When:  every storage operation runs
// Then:  each fails with the context error instead of doing work
func Test_Operations_when_ContextCanceled_then_ReturnCtxError(t *testing.T) {
	s, _ := newStore(t, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	digest := digestOf([]byte("x"))

	if _, err := s.Put(ctx, digest, bytes.NewReader([]byte("x"))); !errors.Is(err, context.Canceled) {
		t.Fatalf("Put err = %v, want context.Canceled", err)
	}
	if _, err := s.Get(ctx, digest); !errors.Is(err, context.Canceled) {
		t.Fatalf("Get err = %v, want context.Canceled", err)
	}
	if _, err := s.Exists(ctx, digest); !errors.Is(err, context.Canceled) {
		t.Fatalf("Exists err = %v, want context.Canceled", err)
	}
	if _, err := s.Size(ctx, digest); !errors.Is(err, context.Canceled) {
		t.Fatalf("Size err = %v, want context.Canceled", err)
	}
	if err := s.Delete(ctx, digest); !errors.Is(err, context.Canceled) {
		t.Fatalf("Delete err = %v, want context.Canceled", err)
	}
	if err := s.List(ctx, func(string) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("List err = %v, want context.Canceled", err)
	}
}

// Given: digests that would escape the two-level blob layout
// When:  they reach Put, Get and Delete
// Then:  the calls fail safely and nothing is written outside the root
func Test_Digests_when_PathHostile_then_ContainedWithinRoot(t *testing.T) {
	s, root := newStore(t, 0)
	ctx := context.Background()
	hostile := []string{
		"",                  // names nothing
		"a",                 // too short to split
		"..x",               // prefix element is ".."
		"../evil",           // walks above the root
		"ab/../../evil",     // body walks above the root
		"ab/c",              // body nests below the layout
		"sh/../../../etc/p", // deep escape
	}

	for _, d := range hostile {
		if _, err := s.Put(ctx, d, bytes.NewReader([]byte("payload"))); !errors.Is(err, storage.ErrBlobInvalid) {
			t.Errorf("Put(%q) err = %v, want ErrBlobInvalid", d, err)
		}
		if _, err := s.Get(ctx, d); !errors.Is(err, storage.ErrBlobNotFound) {
			t.Errorf("Get(%q) err = %v, want ErrBlobNotFound", d, err)
		}
		if err := s.Delete(ctx, d); !errors.Is(err, storage.ErrBlobNotFound) {
			t.Errorf("Delete(%q) err = %v, want ErrBlobNotFound", d, err)
		}
		if ok, err := s.Exists(ctx, d); err != nil || ok {
			t.Errorf("Exists(%q) = %v, %v; want false, nil", d, ok, err)
		}
	}

	// The temp parent of root must hold nothing but the root directory.
	parent := filepath.Dir(root)
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatalf("read parent: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(root) {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("parent dir holds %v, want only the root", names)
	}
	assertNoFiles(t, root)
}

// Given: one store shared by concurrent goroutines
// When:  they Put distinct blobs, Put the same blob, and read it back
// Then:  no data race or corruption (verified under -race)
func Test_Concurrent_when_SharedStore_then_RaceFree(t *testing.T) {
	s, _ := newStore(t, 0)
	ctx := context.Background()
	shared := makeBlob(4096)
	sharedDigest := digestOf(shared)

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 8; i++ {
				body := makeBlob(1024 + g*16 + i)
				d := digestOf(body)
				if _, err := s.Put(ctx, d, bytes.NewReader(body)); err != nil {
					t.Errorf("Put %s: %v", d, err)
					return
				}
				rc, err := s.Get(ctx, d)
				if err != nil {
					t.Errorf("Get %s: %v", d, err)
					return
				}
				got, err := io.ReadAll(rc)
				if err != nil {
					t.Errorf("read %s: %v", d, err)
					return
				}
				_ = rc.Close()
				if !bytes.Equal(got, body) {
					t.Errorf("round-trip %s corrupted", d)
					return
				}

				// All goroutines publish the same blob: the rename must stay
				// atomic under collision.
				if _, err := s.Put(ctx, sharedDigest, bytes.NewReader(shared)); err != nil {
					t.Errorf("shared Put: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	rc, err := s.Get(ctx, sharedDigest)
	if err != nil {
		t.Fatalf("Get shared: %v", err)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read shared: %v", err)
	}
	_ = rc.Close()
	if !bytes.Equal(got, shared) {
		t.Fatalf("shared blob corrupted after concurrent Puts")
	}
}

// Given: a store and a reader that fails mid-stream
// When:  Put copies the broken stream
// Then:  the read error surfaces and no blob is left behind
func Test_Put_when_ReaderFails_then_ReadErrorAndNoBlob(t *testing.T) {
	s, root := newStore(t, 0)
	digest := digestOf([]byte("irrelevant"))

	_, err := s.Put(context.Background(), digest, io.MultiReader(
		bytes.NewReader([]byte("partial")),
		errReader{errors.New("connection reset")},
	))
	if err == nil || err.Error() != "connection reset" {
		t.Fatalf("Put err = %v, want the reader's error", err)
	}
	assertNoFiles(t, root)
	assertNoTempFiles(t, root)
}

// errReader fails on first read.
type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }
