package gc_test

import (
	"fmt"
	"testing"

	"github.com/BRAVO68WEB/wharf/internal/gc"
)

// Progress and batching tests (plan task 19's progress callback and bounded
// delete batches). Fixtures live in gc_fixtures_test.go.

func Test_Run_when_progress_then_planTickThenOnePerBatch(t *testing.T) {
	// Given: 5 unreferenced blobs and a delete-batch bound of 2.
	ctx := t.Context()
	q := gcTestDB(t)
	store := newGCTestStore()
	for i := range 5 {
		gcTestPut(t, store, fmt.Sprintf("orphan-%d", i))
	}
	var ticks []gc.Progress
	runner := gc.NewRunner(q, store, gc.WithBatchSize(2))

	// When: a sweep runs with a progress callback.
	res, err := runner.Run(ctx, gc.WithProgress(func(p gc.Progress) {
		ticks = append(ticks, p)
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Then: one plan tick, then one sweep tick per batch (2+2+1).
	if len(ticks) != 4 {
		t.Fatalf("progress ticks = %d (%v), want 4", len(ticks), ticks)
	}
	if ticks[0].Phase != gc.PhasePlan || ticks[0].Total != 5 {
		t.Fatalf("plan tick = %+v, want PhasePlan with Total 5", ticks[0])
	}
	for i, want := range []int{2, 4, 5} {
		tick := ticks[i+1]
		if tick.Phase != gc.PhaseSweep || tick.Processed != want {
			t.Fatalf("sweep tick %d = %+v, want PhaseSweep with Processed %d", i, tick, want)
		}
	}

	// And: the terminal tick carries the result totals.
	last := ticks[len(ticks)-1]
	if last.Deleted != len(res.Deleted) || last.Failed != res.Failed || last.BytesFreed != res.BytesFreed {
		t.Fatalf("final tick = %+v, want to match result %+v", last, res)
	}
}

func Test_Run_when_noCandidates_then_singlePlanTick(t *testing.T) {
	// Given: an empty blob store with nothing to collect.
	ctx := t.Context()
	q := gcTestDB(t)
	store := newGCTestStore()
	var ticks []gc.Progress

	// When: a sweep runs with a progress callback.
	res, err := gc.NewRunner(q, store).Run(ctx, gc.WithProgress(func(p gc.Progress) {
		ticks = append(ticks, p)
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Then: the single plan tick is the whole run and nothing is deleted.
	if len(ticks) != 1 || ticks[0].Phase != gc.PhasePlan || ticks[0].Total != 0 {
		t.Fatalf("ticks = %v, want one PhasePlan tick with Total 0", ticks)
	}
	if len(res.Deleted) != 0 || res.BytesFreed != 0 || res.Failed != 0 {
		t.Fatalf("result = %+v, want empty", res)
	}
}

func Test_WithBatchSize_when_nonPositive_then_defaultBoundUsed(t *testing.T) {
	// Given: 3 unreferenced blobs and a non-positive batch bound.
	ctx := t.Context()
	q := gcTestDB(t)
	store := newGCTestStore()
	for _, c := range []string{"orphan-a", "orphan-b", "orphan-c"} {
		gcTestPut(t, store, c)
	}
	var ticks []gc.Progress
	runner := gc.NewRunner(q, store, gc.WithBatchSize(0))

	// When: a sweep runs with a progress callback.
	res, err := runner.Run(ctx, gc.WithProgress(func(p gc.Progress) {
		ticks = append(ticks, p)
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Then: the default bound applies — all 3 fit in one sweep tick and the
	// run completed (a zero bound would never advance the loop).
	if len(res.Deleted) != 3 {
		t.Fatalf("Deleted = %v, want 3 blobs", res.Deleted)
	}
	sweep := 0
	for _, p := range ticks {
		if p.Phase == gc.PhaseSweep {
			sweep++
		}
	}
	if sweep != 1 {
		t.Fatalf("sweep ticks = %d (%v), want 1 (default bound swallows 3 blobs)", sweep, ticks)
	}
}
