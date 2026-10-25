package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// stubDrainer records the Drain call the shutdown sequence must make.
type stubDrainer struct {
	mu      sync.Mutex
	drained bool
}

func (d *stubDrainer) Drain() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.drained = true
}

func (d *stubDrainer) wasDrained() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.drained
}

// blockingHandler runs one request at a time: it signals entered, then waits
// for release before answering. Tests drive the exact in-flight window.
type blockingHandler struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingHandler() *blockingHandler {
	return &blockingHandler{entered: make(chan struct{}), release: make(chan struct{})}
}

func (h *blockingHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	h.once.Do(func() { close(h.entered) })
	<-h.release
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("done"))
}

// newTestServer serves handler on 127.0.0.1:0 and returns the server plus its
// local address.
func newTestServer(t *testing.T, handler http.Handler, drain drainer, timeout time.Duration) (*server, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := newServer(ln, handler, drain)
	s.shutdownTimeout = timeout
	t.Cleanup(func() {
		_ = s.http.Close()
		closeAll(t, handler)
	})
	return s, ln.Addr().String()
}

// closeAll releases any blocking handler still waiting so goroutines exit.
func closeAll(t *testing.T, handler http.Handler) {
	t.Helper()
	if b, ok := handler.(*blockingHandler); ok {
		select {
		case <-b.release:
		default:
			close(b.release)
		}
	}
}

// Given: one request is in flight when the context is cancelled
// When:  the drain runs
// Then:  Serve waits for that request to finish (it answers "done"), marks
//
//	readiness drained, and returns no error
func Test_Serve_when_requestInFlight_then_drainedNotAborted(t *testing.T) {
	handler := newBlockingHandler()
	drain := &stubDrainer{}
	s, addr := newTestServer(t, handler, drain, 30*time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() { serveDone <- s.Serve(ctx) }()

	respCh := make(chan *http.Response, 1)
	go func() {
		resp, err := http.Get("http://" + addr + "/v2/repo/blobs/sha256:abc")
		if err != nil {
			t.Errorf("request: %v", err)
			respCh <- nil
			return
		}
		respCh <- resp
	}()

	<-handler.entered
	cancel()

	select {
	case err := <-serveDone:
		t.Fatalf("Serve returned %v while a request was still in flight", err)
	case <-time.After(100 * time.Millisecond):
		// still draining, as required
	}

	close(handler.release)

	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("Serve = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after the in-flight request finished")
	}

	resp := <-respCh
	if resp == nil {
		t.Fatal("in-flight request did not survive the drain")
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "done" {
		t.Fatalf("body = %q, want %q", body, "done")
	}
	if !drain.wasDrained() {
		t.Fatal("readiness was not marked draining at shutdown start")
	}
}

// Given: an upload-session request is still being written when the drain
//
//	deadline expires
//
// When:  the shutdown timeout fires
// Then:  Serve aborts the remaining connections, reports the aborted upload
//
//	count in the error, and returns
func Test_Serve_when_uploadExceedsDeadline_then_abortedCleanly(t *testing.T) {
	handler := newBlockingHandler()
	drain := &stubDrainer{}
	s, addr := newTestServer(t, handler, drain, 50*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() { serveDone <- s.Serve(ctx) }()

	go func() {
		resp, err := http.Post("http://"+addr+"/v2/repo/blobs/uploads/", "application/octet-stream", nil)
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	<-handler.entered
	cancel()

	select {
	case err := <-serveDone:
		if err == nil {
			t.Fatal("Serve = nil, want deadline error")
		}
		if !strings.Contains(err.Error(), "abort") {
			t.Fatalf("Serve error = %q, want it to mention the abort", err)
		}
		if !strings.Contains(err.Error(), "1 in-flight upload") {
			t.Fatalf("Serve error = %q, want it to report 1 in-flight upload", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after the drain deadline")
	}

	if got := s.uploads.Active(); got != 1 {
		t.Fatalf("Active() = %d, want 1 while the upload is blocked", got)
	}
}

// Given: no request is in flight
// When:  the context is cancelled
// Then:  Serve returns nil promptly and readiness is already draining
func Test_Serve_when_idle_then_cleanReturn(t *testing.T) {
	handler := newBlockingHandler()
	drain := &stubDrainer{}
	s, _ := newTestServer(t, handler, drain, 30*time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Serve(ctx); err != nil {
		t.Fatalf("Serve = %v, want nil", err)
	}
	if !drain.wasDrained() {
		t.Fatal("readiness was not marked draining")
	}
}

// Given: requests to upload-session and non-upload paths
// When:  they pass through the tracker
// Then:  only upload-session requests are counted
func Test_uploadTracker_when_nonUploadRequest_then_notCounted(t *testing.T) {
	var tr uploadTracker
	var during []int
	h := tr.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		during = append(during, tr.Active())
		w.WriteHeader(http.StatusOK)
	}))

	req, _ := http.NewRequest("POST", "/v2/repo/blobs/uploads/", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got := tr.Active(); got != 0 {
		t.Fatalf("Active() after upload = %d, want 0", got)
	}

	req, _ = http.NewRequest("GET", "/v2/repo/blobs/sha256:abc", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	want := []int{1, 0}
	if len(during) != 2 || during[0] != want[0] || during[1] != want[1] {
		t.Fatalf("Active() during requests = %v, want %v", during, want)
	}
}
