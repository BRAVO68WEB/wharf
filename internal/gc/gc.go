package gc

import (
	"context"
	"errors"
	"log/slog"

	"github.com/user/better-docker-registry/internal/db"
	"github.com/user/better-docker-registry/internal/storage"
)

const (
	// gcDefaultBatchSize is the delete-batch bound when the caller supplies
	// none (config gc.batch_size is validated > 0 at the config boundary).
	gcDefaultBatchSize = 64
	// gcDeleteWorkers bounds the deletes one batch has in flight. The sweep
	// runs quiesced, so the parallelism only overlaps backend latency —
	// it never races a registry write.
	gcDeleteWorkers = 8
)

// Blob is one storage blob GC reports on: its content digest and byte size.
type Blob struct {
	Digest string
	Size   int64
}

// Stats is the collection summary the dashboard shows (plan task 18): how
// much of the blob store is still referenced and how much garbage is sitting
// in it.
type Stats struct {
	TotalBlobs        int64
	ReferencedBlobs   int64
	UnreferencedBlobs int64
	UnreferencedBytes int64
}

// Result is the outcome of a real sweep. Deleted holds exactly the blobs
// removed (the same set a DryRun reported, smallest digest first); Failed
// counts candidates the backend refused to delete — those stay for the next
// run.
type Result struct {
	Deleted    []Blob
	BytesFreed int64
	Failed     int
}

// ProgressPhase names the phase a progress tick comes from.
type ProgressPhase int

const (
	// PhasePlan ticks once the candidate set is known, before any deletion.
	PhasePlan ProgressPhase = iota
	// PhaseSweep ticks after each delete batch has been processed.
	PhaseSweep
)

// Progress is one tick of a running collection, shaped for a UI progress
// display: what is planned (Total) and how far the sweep has got.
type Progress struct {
	Phase      ProgressPhase
	Total      int
	Processed  int
	Deleted    int
	Failed     int
	BytesFreed int64
}

// ProgressFunc receives progress ticks from Run. It is called on Run's
// goroutine between batches (never concurrently with itself) and must not
// block long: the sweep stalls while it runs.
type ProgressFunc func(Progress)

// RunnerOption tunes a Runner at construction.
type RunnerOption func(*Runner)

// WithBatchSize bounds how many blobs one delete batch holds (plan task 19's
// "batch deletes"). A non-positive n keeps the default bound.
func WithBatchSize(n int) RunnerOption {
	return func(r *Runner) {
		if n > 0 {
			r.batchSize = n
		}
	}
}

// RunOption tunes one Run call.
type RunOption func(*runConfig)

// runConfig collects the per-run knobs WithProgress sets.
type runConfig struct {
	progress ProgressFunc
}

// WithProgress reports sweep progress to fn (plan task 19's "progress
// callback for UI"). The caller keeps the callback; Run only invokes it.
func WithProgress(fn ProgressFunc) RunOption {
	return func(c *runConfig) { c.progress = fn }
}

// Runner executes mark-and-sweep garbage collection (plan task 19): mark the
// digests every stored manifest still needs (plus what an in-flight upload
// still claims), sweep the rest out of blob storage in bounded delete
// batches. It is the engine behind the dashboard's dry-run and run buttons.
//
// Mark reads the manifests table, not blob_refs: deleting a manifest leaves
// its blob_refs edges behind on purpose (task 9), so those edges are a
// superset that would retain garbage forever. The manifest payloads are the
// live reference graph.
type Runner struct {
	q         db.Querier
	store     storage.Storage
	batchSize int
}

// NewRunner returns a collector over the metadata in q and the blobs in
// store. The caller is responsible for quiescing writes around Run (see
// Lock).
func NewRunner(q db.Querier, store storage.Storage, opts ...RunnerOption) *Runner {
	r := &Runner{q: q, store: store, batchSize: gcDefaultBatchSize}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Stats reports the summary numbers without deleting anything.
func (r *Runner) Stats(ctx context.Context) (Stats, error) {
	p, err := r.plan(ctx)
	if err != nil {
		return Stats{}, err
	}
	s := Stats{
		TotalBlobs:        p.total,
		ReferencedBlobs:   p.total - int64(len(p.candidates)),
		UnreferencedBlobs: int64(len(p.candidates)),
	}
	for _, b := range p.candidates {
		s.UnreferencedBytes += b.Size
	}
	return s, nil
}

// DryRun lists exactly the blobs Run would delete, smallest digest first,
// without touching the store.
func (r *Runner) DryRun(ctx context.Context) ([]Blob, error) {
	p, err := r.plan(ctx)
	if err != nil {
		return nil, err
	}
	return p.candidates, nil
}

// Run sweeps every unreferenced blob out of storage and drops its metadata
// and repo-reference edges. Candidates are deleted in batches of at most
// batchSize and fn (WithProgress) ticks once after planning and once per
// batch — with no candidates the single PhasePlan tick is the whole run.
// Deletion is not idempotent in storage (task 4 contract), so a blob that
// vanishes under us still counts as collected.
func (r *Runner) Run(ctx context.Context, opts ...RunOption) (Result, error) {
	var cfg runConfig
	for _, o := range opts {
		o(&cfg)
	}
	p, err := r.plan(ctx)
	if err != nil {
		return Result{}, err
	}

	var res Result
	report(cfg.progress, Progress{Phase: PhasePlan, Total: len(p.candidates)})
	for start := 0; start < len(p.candidates); start += r.batchSize {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		end := min(start+r.batchSize, len(p.candidates))
		batch := p.candidates[start:end]
		for i, delErr := range r.sweepBatch(ctx, batch) {
			b := batch[i]
			if delErr != nil && !errors.Is(delErr, storage.ErrBlobNotFound) {
				slog.Warn("gc: blob delete failed", "digest", b.Digest, "err", delErr)
				res.Failed++
				continue
			}
			r.forget(ctx, b.Digest)
			res.Deleted = append(res.Deleted, b)
			res.BytesFreed += b.Size
			slog.Info("gc: deleted unreferenced blob", "digest", b.Digest, "size", b.Size)
		}
		report(cfg.progress, Progress{
			Phase:      PhaseSweep,
			Total:      len(p.candidates),
			Processed:  end,
			Deleted:    len(res.Deleted),
			Failed:     res.Failed,
			BytesFreed: res.BytesFreed,
		})
	}
	return res, nil
}

// report delivers one progress tick, tolerating an absent callback.
func report(fn ProgressFunc, p Progress) {
	if fn != nil {
		fn(p)
	}
}
