package s3_test

// List semantics and pagination tests.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
)

// Given: a store with no blobs
// When:  List is called
// Then:  the callback is never invoked and List reports success
func Test_List_when_StoreEmpty_then_NoCallbacks(t *testing.T) {
	store := newStore(t)
	calls := 0
	err := store.List(context.Background(), func(digest string) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if calls != 0 {
		t.Fatalf("List called fn %d times, want 0", calls)
	}
}

// Given: a store holding three blobs
// When:  List is called with a collecting callback
// Then:  every digest is visited exactly once
func Test_List_when_StorePopulated_then_VisitsEveryDigest(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	want := map[string]bool{}
	for _, body := range [][]byte{[]byte("a"), []byte("bb"), []byte("ccc")} {
		digest := digestOf(body)
		want[digest] = true
		if _, err := store.Put(ctx, digest, bytes.NewReader(body)); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}

	seen := map[string]int{}
	err := store.List(ctx, func(digest string) error {
		seen[digest]++
		return nil
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(seen) != len(want) {
		t.Fatalf("List visited %d digests, want %d (%v)", len(seen), len(want), seen)
	}
	for d := range want {
		if seen[d] != 1 {
			t.Fatalf("List visited %s %d times, want 1", d, seen[d])
		}
	}
}

// Given: a store holding one blob and a callback that fails
// When:  List is called
// Then:  iteration stops and the callback's error comes back unwrapped
func Test_List_when_CallbackErrors_then_ReturnsItUnwrapped(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	body, digest := blobOf(16)
	if _, err := store.Put(ctx, digest, bytes.NewReader(body)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	sentinel := errors.New("stop now")
	visits := 0
	err := store.List(ctx, func(string) error {
		visits++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("List err = %v, want the callback error", err)
	}
	// "returned unwrapped" means identity, not merely errors.Is.
	if err != sentinel {
		t.Fatalf("List err = %v, want it returned unwrapped (same value)", err)
	}
	if visits != 1 {
		t.Fatalf("List visited %d times, want 1", visits)
	}
}

// Given: a store holding blobs
// When:  the List callback reads back from the same store
// Then:  it succeeds — the contract forbids holding internal locks in there
func Test_List_when_CallbackCallsBack_then_StoreUsable(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	body, digest := blobOf(24)
	if _, err := store.Put(ctx, digest, bytes.NewReader(body)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	err := store.List(ctx, func(d string) error {
		ok, err := store.Exists(ctx, d)
		if err != nil {
			return fmt.Errorf("Exists(%s) in callback: %w", d, err)
		}
		if !ok {
			return fmt.Errorf("Exists(%s) = false inside List", d)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
}

// Given: a store holding more objects than one ListObjectsV2 page returns
// When:  List is called
// Then:  every object is visited exactly once, proving ContinuationToken
//
//	pagination is wired up
func Test_List_when_MoreThan1000Objects_then_Paginates(t *testing.T) {
	if testing.Short() {
		t.Skip("pagination test stores 1250 objects; skipped in -short")
	}

	store := newStore(t)
	ctx := context.Background()
	const total = 1250 // ListObjectsV2 pages cap at 1000 keys

	want := make(map[string]bool, total)
	for i := 0; i < total; i++ {
		body := []byte(fmt.Sprintf("pagination-object-%d", i))
		digest := digestOf(body)
		want[digest] = true
		if _, err := store.Put(ctx, digest, bytes.NewReader(body)); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
	}

	seen := make(map[string]int, total)
	err := store.List(ctx, func(digest string) error {
		seen[digest]++
		return nil
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(seen) != total {
		t.Fatalf("List visited %d digests, want %d", len(seen), total)
	}
	for d := range want {
		if seen[d] != 1 {
			t.Fatalf("List visited %s %d times, want 1", d, seen[d])
		}
	}
	for d, n := range seen {
		if !want[d] {
			t.Fatalf("List visited unknown digest %s %d times", d, n)
		}
	}
}

// Given: a store holding blobs
// When:  List is called with a context that is already cancelled
// Then:  it fails rather than walking the whole bucket
func Test_List_when_ContextCancelled_then_Fails(t *testing.T) {
	store := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := store.List(ctx, func(string) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("List err = %v, want it to match context.Canceled", err)
	}
}
