package db

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentReadWrite: Given one shared database, When writer goroutines
// insert repositories while reader goroutines query the catalog, Then every
// operation succeeds — WAL plus busy_timeout must absorb the contention.
// Run under -race per the acceptance criteria.
func TestConcurrentReadWrite(t *testing.T) {
	q := New(newTestDB(t))
	ctx := t.Context()

	const (
		writers       = 8
		perWriter     = 25
		readers       = 4
		perReader     = 50
		expectedRepos = writers * perWriter
	)

	var wg sync.WaitGroup
	errCh := make(chan error, writers*perWriter+readers*perReader)

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				name := fmt.Sprintf("repo/writer-%d/item-%d", w, i)
				if _, err := q.CreateRepository(ctx, name); err != nil {
					errCh <- fmt.Errorf("writer %d insert %q: %w", w, name, err)
					return
				}
			}
		}(w)
	}

	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			for i := 0; i < perReader; i++ {
				if _, err := q.CountRepositories(ctx); err != nil {
					errCh <- fmt.Errorf("reader %d count: %w", r, err)
					return
				}
				if _, err := q.ListRepositoriesFromStart(ctx, 10); err != nil {
					errCh <- fmt.Errorf("reader %d list: %w", r, err)
					return
				}
			}
		}(r)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
	if t.Failed() {
		t.FailNow()
	}

	n, err := q.CountRepositories(ctx)
	if err != nil {
		t.Fatalf("final CountRepositories: %v", err)
	}
	if n != expectedRepos {
		t.Errorf("repository count = %d, want %d (lost or duplicated writes)", n, expectedRepos)
	}
}
