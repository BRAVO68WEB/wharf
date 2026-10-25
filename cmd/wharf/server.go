package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

const (
	// readHeaderTimeout bounds slowloris-style clients. Read/Write timeouts
	// come from config (server.read_timeout / server.write_timeout) and
	// default to zero so large blob uploads and downloads are never cut off.
	readHeaderTimeout = 10 * time.Second

	// shutdownTimeout is the drain budget on SIGINT/SIGTERM: in-flight
	// requests (including blob upload writes) get 30s to finish before the
	// remaining connections are aborted.
	shutdownTimeout = 30 * time.Second
)

// drainer is the readiness-drain seam. *api.Health implements it: shutdown
// flips /readyz to 503 so orchestrators stop routing here while in-flight
// work drains. Narrow so the shutdown sequence tests without a live database.
type drainer interface{ Drain() }

// uploadTracker counts in-flight blob-upload requests so shutdown can report
// exactly the push streams still being written when the drain deadline
// expires. Sessions span several requests (POST to open, PATCH for chunks,
// PUT to commit); each of those is counted while it runs.
type uploadTracker struct{ active atomic.Int64 }

// Wrap returns handler with upload-session accounting in front of it.
func (t *uploadTracker) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isUploadRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		t.active.Add(1)
		defer t.active.Add(-1)
		next.ServeHTTP(w, r)
	})
}

// Active returns the number of upload-session requests currently in flight.
func (t *uploadTracker) Active() int { return int(t.active.Load()) }

// isUploadRequest reports whether r addresses a blob upload session under
// some repository ("/v2/{name}/blobs/uploads/..."). Ordinary blob pulls
// ("/blobs/{digest}") do not match.
func isUploadRequest(r *http.Request) bool {
	return strings.Contains(r.URL.Path, "/blobs/uploads")
}

// server owns the HTTP listener and the shutdown sequence: readiness drain,
// in-flight request drain, and deadline-abort accounting.
type server struct {
	http    *http.Server
	ln      net.Listener
	drain   drainer
	uploads *uploadTracker

	// shutdownTimeout bounds the drain. Production uses shutdownTimeout;
	// tests shrink it.
	shutdownTimeout time.Duration
}

// newServer returns a server over ln serving handler with upload accounting
// in front. Read and write timeouts stay zero (config sets them at the call
// site): the registry streams multi-GB blobs and a write timeout would
// truncate them.
func newServer(ln net.Listener, handler http.Handler, drain drainer) *server {
	uploads := &uploadTracker{}
	return &server{
		http: &http.Server{
			Handler:           uploads.Wrap(handler),
			ReadHeaderTimeout: readHeaderTimeout,
		},
		ln:              ln,
		drain:           drain,
		uploads:         uploads,
		shutdownTimeout: shutdownTimeout,
	}
}

// Serve runs the server until ctx is cancelled (SIGINT/SIGTERM in
// production), then shuts down gracefully:
//
//  1. readiness fails (/readyz -> 503) so new traffic stops arriving;
//  2. http.Server.Shutdown drains in-flight requests — including upload
//     chunks being written — up to the deadline;
//  3. on deadline, the remaining connections are aborted and the error
//     reports how many upload sessions were cut mid-write.
//
// It returns nil on a clean drain and an error when the deadline forced an
// abort (or the listener failed before shutdown).
func (s *server) Serve(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		slog.Info("http server listening", "addr", s.ln.Addr().String())
		errCh <- s.http.Serve(s.ln)
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}

	s.drain.Drain()
	slog.Info("shutting down", "drain_timeout", s.shutdownTimeout)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
	defer cancel()
	if err := s.http.Shutdown(shutdownCtx); err == nil {
		slog.Info("shutdown complete")
		return nil
	}

	aborted := s.uploads.Active()
	slog.Error("drain deadline exceeded, aborting remaining connections",
		"drain_timeout", s.shutdownTimeout, "in_flight_uploads", aborted)
	_ = s.http.Close()
	return fmt.Errorf("shutdown deadline exceeded after %s: aborted %d in-flight upload(s)",
		s.shutdownTimeout, aborted)
}
