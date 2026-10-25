package api_test

import (
	"database/sql"
	"errors"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/BRAVO68WEB/wharf/internal/api"
	"github.com/BRAVO68WEB/wharf/internal/db"
)

// newHealthFixture builds a Health over a real SQLite database and an
// in-memory store: readiness must exercise the same probe path the process
// runs in production, not a mock that answers whatever the test wants.
func newHealthFixture(t *testing.T) (*api.Health, *fakeStorage, *sql.DB) {
	t.Helper()
	sqlDB, err := db.OpenAndMigrate(t.Context(), filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	store := newFakeStorage()
	return api.NewHealth(sqlDB, store), store, sqlDB
}

func healthMux(h *api.Health) *http.ServeMux {
	mux := http.NewServeMux()
	h.Mount(mux)
	return mux
}

// Given: the process is up and no shutdown has begun
// When:  GET /healthz
// Then:  200 with body "ok" — liveness must not depend on any dependency
func Test_liveness_when_processUp_then_ok(t *testing.T) {
	h, _, _ := newHealthFixture(t)

	rec := serve(t, healthMux(h), "GET", "/healthz", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != "ok\n" {
		t.Fatalf("body = %q, want %q", got, "ok\n")
	}
	assertHeader(t, rec.Header(), "Content-Type", "text/plain; charset=utf-8")
}

// Given: the process is draining after a shutdown signal
// When:  GET /healthz
// Then:  200 — the process is still alive; only readiness reports the drain
func Test_liveness_when_draining_then_ok(t *testing.T) {
	h, _, _ := newHealthFixture(t)
	h.Drain()

	rec := serve(t, healthMux(h), "GET", "/healthz", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// Given: database and blob store both answer their probes
// When:  GET /readyz
// Then:  200 with body "ok"
func Test_readiness_when_dependenciesReachable_then_ok(t *testing.T) {
	h, _, _ := newHealthFixture(t)

	rec := serve(t, healthMux(h), "GET", "/readyz", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != "ok\n" {
		t.Fatalf("body = %q, want %q", got, "ok\n")
	}
}

// Given: the metadata database no longer answers
// When:  GET /readyz
// Then:  503 — a registry that cannot read metadata cannot serve pulls
func Test_readiness_when_dbUnreachable_then_unavailable(t *testing.T) {
	h, _, sqlDB := newHealthFixture(t)
	_ = sqlDB.Close()

	rec := serve(t, healthMux(h), "GET", "/readyz", nil)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if got := rec.Body.String(); got != "unavailable\n" {
		t.Fatalf("body = %q, want %q", got, "unavailable\n")
	}
}

// Given: the blob store probe fails (backend down)
// When:  GET /readyz
// Then:  503
func Test_readiness_when_storeUnreachable_then_unavailable(t *testing.T) {
	h, store, _ := newHealthFixture(t)
	store.fail = errors.New("backend down")

	rec := serve(t, healthMux(h), "GET", "/readyz", nil)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

// Given: a shutdown signal has begun draining the process
// When:  GET /readyz
// Then:  503 — load balancers must stop routing new traffic to this replica
func Test_readiness_when_draining_then_unavailable(t *testing.T) {
	h, _, _ := newHealthFixture(t)
	h.Drain()

	rec := serve(t, healthMux(h), "GET", "/readyz", nil)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}
