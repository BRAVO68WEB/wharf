package gc

import (
	"context"
	"log/slog"
	"sort"
	"sync"

	"github.com/user/better-docker-registry/internal/db"
)

// gcPlan is one mark-and-sweep planning pass: the blob inventory size and the
// exact candidate set, sorted by digest so dry-run output and deletion order
// are deterministic.
type gcPlan struct {
	total      int64
	candidates []Blob
}

// plan runs mark against the manifest graph and diffs it over the blob
// inventory. Every public entry point shares this walk, so the dry-run list
// and the real sweep can never disagree.
func (r *Runner) plan(ctx context.Context) (gcPlan, error) {
	referenced, err := r.mark(ctx)
	if err != nil {
		return gcPlan{}, err
	}
	var out gcPlan
	err = r.store.List(ctx, func(digest string) error {
		out.total++
		if _, live := referenced[digest]; live {
			return nil
		}
		size, err := r.store.Size(ctx, digest)
		if err != nil {
			return err
		}
		out.candidates = append(out.candidates, Blob{Digest: digest, Size: size})
		return nil
	})
	if err != nil {
		return gcPlan{}, err
	}
	sort.Slice(out.candidates, func(i, j int) bool {
		return out.candidates[i].Digest < out.candidates[j].Digest
	})
	return out, nil
}

// sweepBatch deletes one batch of candidates, at most gcDeleteWorkers deletes
// in flight at once, and returns each delete's error aligned with batch. The
// backend latency of the deletes overlaps instead of adding up; result order
// stays deterministic because the merge reads the indexed outcomes.
func (r *Runner) sweepBatch(ctx context.Context, batch []Blob) []error {
	outcomes := make([]error, len(batch))
	jobs := make(chan int)
	workers := min(gcDeleteWorkers, len(batch))
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for i := range jobs {
				outcomes[i] = r.store.Delete(ctx, batch[i].Digest)
			}
		}()
	}
	for i := range batch {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return outcomes
}

// forget drops the swept blob's metadata row and repo-reference edges. The
// blob is already gone at this point, so cleanup failures are logged, never
// fatal — the next run finds nothing left to sweep for this digest.
func (r *Runner) forget(ctx context.Context, digest string) {
	if err := r.q.DeleteBlobMetadata(ctx, digest); err != nil {
		slog.Warn("gc: blob metadata delete failed", "digest", digest, "err", err)
	}
	repos, err := r.q.ListReposForBlob(ctx, digest)
	if err != nil {
		slog.Warn("gc: blob ref lookup failed", "digest", digest, "err", err)
		return
	}
	for _, repoID := range repos {
		if err := r.q.DeleteBlobRef(ctx, db.DeleteBlobRefParams{Digest: digest, RepoID: repoID}); err != nil {
			slog.Warn("gc: blob ref delete failed", "digest", digest, "repo_id", repoID, "err", err)
		}
	}
}
