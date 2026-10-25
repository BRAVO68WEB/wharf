package gc

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCollectSkipsActiveRun(t *testing.T) {
	lock := NewLock()
	release, ok := lock.BeginGC()
	if !ok {
		t.Fatal("BeginGC refused")
	}
	defer release()

	called := false
	ran, err := Collect(context.Background(), lock, func(context.Context) error {
		called = true
		return nil
	})
	if ran || err != nil || called {
		t.Fatalf("ran=%v err=%v called=%v, want a skipped run", ran, err, called)
	}
}

func TestCollectRunsOnce(t *testing.T) {
	lock := NewLock()
	n := 0
	ran, err := Collect(context.Background(), lock, func(context.Context) error {
		n++
		return errors.New("stop")
	})
	if !ran || err == nil || n != 1 {
		t.Fatalf("ran=%v err=%v n=%d", ran, err, n)
	}
	if lock.Running() {
		t.Fatal("lock still held")
	}
}

func TestStartScheduleZeroDoesNotRun(t *testing.T) {
	called := false
	StartSchedule(context.Background(), 0, func(context.Context) error {
		called = true
		return nil
	})
	time.Sleep(20 * time.Millisecond)
	if called {
		t.Fatal("zero interval started a collection")
	}
}
