package gc

import (
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/user/better-docker-registry/internal/storage"
)

// Lock is the GC quiesce lock (plan tasks 18-19). A collection takes it
// exclusively while /v2 write requests take it shared:
//
//   - BeginGC starts a collection. It rejects a second concurrent run (a
//     dashboard double-submit must never queue one sweep behind another) and
//     then blocks until in-flight writes drain — "quiesce" in the literal
//     sense: writes stop, the sweep runs undisturbed.
//   - BeginWrite admits one registry write (push, manifest PUT, blob DELETE).
//     It is non-blocking and reports false while a collection holds or is
//     about to hold the lock, which WriteGuard turns into a 503.
//
// Read requests (pulls) never touch the lock: the plan only quiesces writes,
// and a pull of an about-to-be-collected blob fails with the usual 404.
type Lock struct {
	mu sync.RWMutex
	// running marks an in-flight collection so a second BeginGC is refused
	// immediately instead of blocking on mu until the first run finishes.
	running atomic.Bool
}

// NewLock returns a quiesce lock with no collection in flight.
func NewLock() *Lock {
	return &Lock{}
}

// BeginGC starts a collection run. ok is false when a run is already in
// flight (the caller must not start a second sweep). Otherwise the returned
// release must be called when the run completes; until then /v2 writes are
// refused and new ones fail closed in BeginWrite.
//
// A write admitted before BeginGC keeps running until it finishes: release
// is only reached after mu.Lock() has drained every such reader, so a sweep
// never deletes under an active upload.
func (l *Lock) BeginGC() (release func(), ok bool) {
	if !l.running.CompareAndSwap(false, true) {
		return nil, false
	}
	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		l.running.Store(false)
	}, true
}

// BeginWrite admits one state-changing registry request. ok is false while a
// collection is running — registered (see Running) or already sweeping — and
// the caller answers 503, which is what the registry protocol expects of a
// busy server. Both checks are non-blocking: a write must never hang behind
// a sweep, and the running flag refuses writes the instant BeginGC is called,
// before mu.Lock() has even been reached (TryRLock alone leaves that window).
//
// The converse race is safe: a write that slips in before BeginGC registers
// holds the read side, so the sweep's mu.Lock() drains it before deleting.
func (l *Lock) BeginWrite() (release func(), ok bool) {
	if l.running.Load() {
		return nil, false
	}
	if !l.mu.TryRLock() {
		return nil, false
	}
	return l.mu.RUnlock, true
}

// Running reports whether a collection is in flight (draining or sweeping).
func (l *Lock) Running() bool {
	return l.running.Load()
}

// WriteGuard wraps /v2 write routes with the quiesce check: safe methods pass
// through untouched, every other method is admitted via BeginWrite or
// answered 503 with the OCI error envelope (code UNAVAILABLE, whose
// HTTPStatus is 503) plus Retry-After. Mount it on the /v2 tree with
// api.WithWriteQuiesce or directly via chi's Use.
func (l *Lock) WriteGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		release, ok := l.BeginWrite()
		if !ok {
			writeQuiesced(w)
			return
		}
		defer release()
		next.ServeHTTP(w, r)
	})
}

// writeQuiesced answers 503 for a write refused during collection. The body
// is the /v2 OCI error envelope, marshalled here rather than via internal/api
// so gc keeps its import graph at db+storage (api mounts gc, never the
// reverse).
func writeQuiesced(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "5")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(storage.ErrorCodeUnavailable.HTTPStatus())
	_ = json.NewEncoder(w).Encode(storage.ErrorResponse{
		Errors: []*storage.Error{
			storage.NewError(storage.ErrorCodeUnavailable, "registry is undergoing garbage collection"),
		},
	})
}
