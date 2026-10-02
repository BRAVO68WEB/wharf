package api

import (
	"context"
	"database/sql"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/user/better-docker-registry/internal/storage"
)

// probeTimeout bounds one dependency probe. A hung backend must answer
// /readyz with 503, not stall the probe until the orchestrator's own timeout.
const probeTimeout = 3 * time.Second

// probeDigest is a syntactically valid digest that can never exist (content
// hashing to all-zero bytes is not a thing anyone pushed). The readiness
// probe asks the store whether it holds it: a reachable backend answers
// (false, nil), an unreachable one answers an error.
const probeDigest = "sha256:" +
	"0000000000000000000000000000000000000000000000000000000000000000"

// Health serves the process health endpoints:
//
//   - GET /healthz — liveness: 200 whenever the process is up. Restarts are
//     never triggered by a dependency outage; that is what /readyz is for.
//   - GET /readyz — readiness: 200 only while every dependency probe passes
//     and no shutdown is in progress. 503 tells the load balancer to stop
//     routing here (drain window, database or blob store down).
type Health struct {
	db    *sql.DB
	store storage.Storage

	// draining marks the shutdown window: readiness fails, liveness does not.
	draining atomic.Bool
}

// NewHealth returns the health endpoints over the metadata database and the
// blob store. Both are probed live on every readiness request; neither is
// cached, so a recovered dependency flips /readyz back to 200 on its own.
func NewHealth(db *sql.DB, store storage.Storage) *Health {
	return &Health{db: db, store: store}
}

// Drain marks the process as shutting down. From here on /readyz answers
// 503 so traffic drains away while in-flight requests finish. Idempotent
// and safe to call concurrently.
func (h *Health) Drain() {
	h.draining.Store(true)
}

// Mount registers GET /healthz and GET /readyz on mux.
func (h *Health) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", h.Liveness)
	mux.HandleFunc("GET /readyz", h.Readiness)
}

// Liveness answers GET /healthz: 200 while the process runs, dependency
// state ignored.
func (h *Health) Liveness(w http.ResponseWriter, _ *http.Request) {
	writeHealth(w, http.StatusOK, "ok\n")
}

// Readiness answers GET /readyz: 200 when the database and the blob store
// both answer their probe and no drain is in progress, 503 otherwise.
func (h *Health) Readiness(w http.ResponseWriter, r *http.Request) {
	if h.draining.Load() {
		writeHealth(w, http.StatusServiceUnavailable, "unavailable\n")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()

	if err := h.db.PingContext(ctx); err != nil {
		writeHealth(w, http.StatusServiceUnavailable, "unavailable\n")
		return
	}
	if _, err := h.store.Exists(ctx, probeDigest); err != nil {
		writeHealth(w, http.StatusServiceUnavailable, "unavailable\n")
		return
	}
	writeHealth(w, http.StatusOK, "ok\n")
}

// writeHealth writes a plain-text health answer. no-store keeps probes from
// being cached by any proxy between us and the orchestrator.
func writeHealth(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}
