package gc

import (
	"context"
	"log/slog"
	"time"
)

// Collect runs fn while holding the quiesce lock. ran is false when a
// collection is already in flight; fn is not called and err is nil.
func Collect(ctx context.Context, lock *Lock, fn func(context.Context) error) (ran bool, err error) {
	release, ok := lock.BeginGC()
	if !ok {
		return false, nil
	}
	defer release()
	return true, fn(ctx)
}

// StartSchedule calls tick every interval until ctx ends. A zero interval
// does nothing. tick owns the quiesce lock. The scheduler does not take it
// again, or a tick would always see itself as already running.
func StartSchedule(ctx context.Context, interval time.Duration, tick func(context.Context) error) {
	if interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := tick(ctx); err != nil {
					slog.Error("gc: scheduled run failed", "err", err)
				}
			}
		}
	}()
}
