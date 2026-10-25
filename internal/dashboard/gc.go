package dashboard

import (
	"github.com/user/better-docker-registry/internal/db"
	"github.com/user/better-docker-registry/internal/gc"
	"github.com/user/better-docker-registry/internal/storage"
)

// GCHandler holds the sweep the dashboard JSON API runs. The React
// Storage page is the only UI. This type does not serve HTML.
type GCHandler struct {
	runner *gc.Runner
	lock   *gc.Lock
}

// NewGCHandler returns a handler over the blobs in store with metadata in q.
// lock is the quiesce lock the run holds exclusively; /v2 write routes must
// share it (api.WithWriteQuiesce) for the 503-during-GC contract to hold.
// runnerOpts tune the engine (gc.WithBatchSize for config gc.batch_size).
func NewGCHandler(q db.Querier, store storage.Storage, lock *gc.Lock, runnerOpts ...gc.RunnerOption) *GCHandler {
	return &GCHandler{runner: gc.NewRunner(q, store, runnerOpts...), lock: lock}
}

// gcBytes sums the sizes of a blob listing.
func gcBytes(blobs []gc.Blob) int64 {
	var total int64
	for _, b := range blobs {
		total += b.Size
	}
	return total
}
